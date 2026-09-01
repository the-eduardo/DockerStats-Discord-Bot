package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do /exec, não Client.Exec isolado (que já tem
// prova própria em internal/dockerx/exec_exitcode_test.go): antes desta
// mudança, handleModal (ops.go) gravava "✅ executado" no canal de auditoria
// mesmo quando o comando terminava com exit code != 0 — o código só era
// anexado ao texto da resposta efêmera, que some, nunca ao auditEntry. Um
// teste que só cobrisse Client.Exec não pegaria isso: o bug estava no call
// site, não na função.

// fakeExecHost sobe um stub da API do Docker que cobre create+attach(hijack)+
// inspect do /exec, devolvendo o exitCode dado.
func fakeExecHost(t *testing.T, exitCode int, running bool) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "deadbeef",
				"Config": map[string]any{"Tty": false},
				"State":  map[string]any{"Status": "running"},
			})
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "exec1"})
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/start"):
			// Drena o corpo da requisição ANTES do hijack — ver comentário
			// equivalente em internal/dockerx/exec_exitcode_test.go.
			_, _ = io.Copy(io.Discard, r.Body)
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "servidor de teste não suporta hijack", http.StatusInternalServerError)
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			sw := stdcopy.NewStdWriter(buf, stdcopy.Stdout)
			_, _ = sw.Write([]byte("saida do comando\n"))
			_ = buf.Flush()
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ExitCode": exitCode, "Running": running})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("main", "Main")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// newExecWiringBot monta um Bot mínimo capaz de rodar handleModal fim-a-fim:
// host fake com hijack, sessão do discordgo apontada para o recordingTransport
// (nada sai para a rede de verdade), cfg sem allow-list e limiter aberto.
func newExecWiringBot(t *testing.T, exitCode int) (*Bot, *recordingTransport) {
	t.Helper()
	return newExecWiringBotRunning(t, exitCode, false)
}

// newExecWiringBotRunning e' a variante de newExecWiringBot que controla o
// campo Running do inspect — usada para provar o guard de exec ainda em
// execucao (ver TestHandleModalAuditaComoNaoConfirmadoQuandoAindaRodando).
func newExecWiringBotRunning(t *testing.T, exitCode int, running bool) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{fakeExecHost(t, exitCode, running)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}, rt
}

// execModalInteraction monta a interação de submissão do modal de /exec, no
// mesmo formato que handleModal espera (CustomID "exec:<hostKey>:<container>",
// valor do campo "cmd" dentro de um ActionsRow/TextInput).
func execModalInteraction(cmd string) *discordgo.InteractionCreate {
	data := discordgo.ModalSubmitInteractionData{
		CustomID: "exec:main:web",
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.TextInput{CustomID: "cmd", Value: cmd},
			}},
		},
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		ID:   "1", AppID: "2", Token: "tok",
		Data: data,
	}}
}

// TestHandleModalAuditaExitCodeReal prova que a auditoria do /exec (o registro
// DURÁVEL, não a resposta efêmera) reflete o exit code de verdade. Antes desta
// mudança este teste falhava: handleModal sempre gravava result="✅ executado"
// (mutação M1 abaixo reproduz exatamente esse bug).
func TestHandleModalAuditaExitCodeReal(t *testing.T) {
	b, rt := newExecWiringBot(t, 3)
	b.cfg.AuditChannelID = "canal-auditoria"
	b.handleModal(execModalInteraction("exit 3"))
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "exit code 3") {
		t.Fatalf("auditoria não registrou o exit code real; corpo enviado: %q", sent)
	}
	if strings.Contains(sent, "executado") && !strings.Contains(sent, "exit code") {
		t.Fatalf("auditoria marcou sucesso apesar do exit code != 0: %q", sent)
	}
	// Regressão exata do bug: "✅ executado" nunca pode aparecer junto de um
	// exit code diferente de zero.
	if strings.Contains(sent, "\\u2705 executado") || strings.Contains(sent, "✅ executado") {
		t.Fatalf("auditoria gravou sucesso (✅ executado) com exit code 3: %q", sent)
	}
}

// TestHandleModalAuditaSucessoComExitZero é a contraprova: exit code 0 tem
// que continuar auditando sucesso, senão o teste acima poderia estar apenas
// proibindo a palavra "executado" incondicionalmente.
func TestHandleModalAuditaSucessoComExitZero(t *testing.T) {
	b, rt := newExecWiringBot(t, 0)
	b.cfg.AuditChannelID = "canal-auditoria"
	b.handleModal(execModalInteraction("echo oi"))
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "executado") {
		t.Fatalf("exit code 0 devia auditar sucesso, corpo: %q", sent)
	}
	if strings.Contains(sent, "exit code") {
		t.Fatalf("exit code 0 não devia mencionar exit code na auditoria: %q", sent)
	}
}

// TestHandleModalAuditaComoNaoConfirmadoQuandoAindaRodando prova a fiação do
// guard de dockerx.Exec: attach ter dado EOF (o stub fecha o stream logo após
// escrever a saída) não pode virar "✅ executado" na auditoria quando o
// inspect ainda responde Running == true — o daemon nem gravou o exit code
// real. Antes do guard em internal/dockerx/ops.go, ExitCode vinha 0 junto de
// Running == true e este teste falhava com "✅ executado" no corpo.
func TestHandleModalAuditaComoNaoConfirmadoQuandoAindaRodando(t *testing.T) {
	b, rt := newExecWiringBotRunning(t, 0, true)
	b.cfg.AuditChannelID = "canal-auditoria"
	b.handleModal(execModalInteraction("exec >/dev/null 2>&1; sleep 60"))
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "não confirmado") {
		t.Fatalf("auditoria não marcou o exec ainda rodando como não confirmado: %q", sent)
	}
	if strings.Contains(sent, "\\u2705 executado") || strings.Contains(sent, "✅ executado") {
		t.Fatalf("auditoria gravou sucesso (✅ executado) com o exec ainda Running: %q", sent)
	}
}
