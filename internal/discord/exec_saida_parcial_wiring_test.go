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

// Este arquivo testa a FIAÇÃO do ramo de ERRO de handleModal (ops.go,
// case err != nil) quando dockerx.Exec devolve saída PARCIAL junto com o erro
// — o único retorno de Exec com out != "" e err != nil (execOutput em
// dockerx/ops.go). Acontece quando o stream do attach quebra NO MEIO: reset de
// TCP no proxy, ssh do master caindo (visto em produção 12/11/2026 03:00:55Z),
// frame Systemerr do daemon. Nesse caso o comando JÁ RODOU no container, e o
// ramo de erro descartava o `out`: o operador ficava sem ver nada do que o
// comando produziu e podia repetir um exec já executado.
//
// Um teste de Client.Exec isolado não pegaria isso — o bug estava no call
// site, não na função. A contraprova de out == "" (erro sem saída nenhuma) já
// existe em TestHandleModalErroDeExecRespeitaOTetoDe2000.
//
// Reusa execModalInteracaoComAlvo/efemeroConteudo/auditResultado/
// recordingTransport dos arquivos irmãos — não redefinir nenhum deles.

// fakeExecHostStreamQuebrado é fakeExecHostSaida com o attach escrevendo DOIS
// frames: um Stdout com `parcial` e, em seguida, um Systemerr com `causa` —
// que faz stdcopy.StdCopy devolver "error from daemon in stream: <causa>"
// depois de já ter copiado o primeiro frame. É a reprodução fiel do contrato
// "out parcial + err" de execOutput. `causa` é parametrizada porque ela é o
// tamanho do ERRO, e o erro é o que come o orçamento de 2000 chars do
// cabeçalho (ver TestHandleModalSaidaParcialCabeNoTetoComErroLongo).
func fakeExecHostStreamQuebrado(t *testing.T, parcial, causa string) *dockerx.Client {
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
			_, _ = stdcopy.NewStdWriter(buf, stdcopy.Stdout).Write([]byte(parcial))
			_, _ = stdcopy.NewStdWriter(buf, stdcopy.Systemerr).Write([]byte(causa))
			_ = buf.Flush()
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/json"):
			// Não deve ser alcançado: Exec devolve no erro de execOutput, antes
			// do ContainerExecInspect.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ExitCode": 0, "Running": false})
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

// novoBotStreamQuebrado monta o Bot do /exec contra o stub de stream quebrado.
func novoBotStreamQuebrado(t *testing.T, rt *recordingTransport, parcial, causa string) *Bot {
	t.Helper()
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{fakeExecHostStreamQuebrado(t, parcial, causa)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}
}

// TestHandleModalPublicaSaidaParcialQuandoStreamQuebra prova que a resposta
// efêmera traz TANTO o erro quanto o que o comando chegou a produzir.
func TestHandleModalPublicaSaidaParcialQuandoStreamQuebra(t *testing.T) {
	rt := &recordingTransport{}
	b := novoBotStreamQuebrado(t, rt, "PARCIAL-MARCA\n", "FALHA-NO-MEIO")

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if !strings.Contains(content, "FALHA-NO-MEIO") {
		t.Fatalf("resposta efêmera não trouxe a causa do erro: %.400q", content)
	}
	if !strings.Contains(content, "PARCIAL-MARCA") {
		t.Fatalf("resposta efêmera descartou a saída parcial do comando que JÁ rodou: %.400q", content)
	}
}

// TestHandleModalSaidaParcialRespeitaOTetoDe2000 prova que a saída parcial
// grande não estoura o Content (400 no PATCH, operador sem resposta) e que o
// FIM da saída — a parte mais relevante de um comando interrompido — sobrevive
// ao corte.
func TestHandleModalSaidaParcialRespeitaOTetoDe2000(t *testing.T) {
	parcial := strings.Repeat("linha de saida parcial do exec antes do stream quebrar no meio\n", 80) + "FIM-PARCIAL"
	if len(parcial) < 5000 {
		t.Fatalf("payload de teste pequeno demais para exercitar o teto: %d bytes", len(parcial))
	}

	rt := &recordingTransport{}
	b := novoBotStreamQuebrado(t, rt, parcial, "FALHA-NO-MEIO")

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("resposta com saída parcial estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "FALHA-NO-MEIO") {
		t.Fatalf("resposta efêmera não trouxe a causa do erro: %.400q", content)
	}
	if !strings.Contains(content, "FIM-PARCIAL") {
		t.Fatalf("o corte da saída parcial não preservou o FIM: %.400q", content)
	}
}

// TestHandleModalSaidaParcialCabeNoTetoComErroLongo é o caso que de fato
// exercita o desconto do cabeçalho no orçamento: com erro CURTO o cap em
// maxBlock (1850) já segura o teto sozinho, e remover o `- len(head)` da conta
// passa despercebido (medido por mutação em 12/11/2026). Com um erro de ~900
// runes — o tamanho real depois do truncateMiddle(200,700) de uma falha de ssh
// remoto — o cabeçalho come metade do orçamento, e só o desconto evita o 400
// no PATCH. A causa (que fica no FIM do erro de ssh) e o fim da saída parcial
// têm que sobreviver aos dois cortes.
func TestHandleModalSaidaParcialCabeNoTetoComErroLongo(t *testing.T) {
	causa := strings.Repeat("z", 3000) + " CAUSA-NO-FIM"
	parcial := strings.Repeat("linha de saida parcial do exec antes do stream quebrar no meio\n", 80) + "FIM-PARCIAL"

	rt := &recordingTransport{}
	b := novoBotStreamQuebrado(t, rt, parcial, causa)

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("erro longo + saída parcial estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "CAUSA-NO-FIM") {
		t.Fatalf("resposta efêmera não preservou a causa no fim do erro: %.400q", content)
	}
	if !strings.Contains(content, "FIM-PARCIAL") {
		t.Fatalf("o corte da saída parcial não preservou o FIM: %.400q", content)
	}
}

// TestHandleModalSaidaParcialComErroMultibyteNaoDerrubaOHandler prova o guard
// `budget >= 100`. O orçamento é contado em BYTES e o truncateMiddle do erro
// corta em RUNES: com um erro de 900 runes multi-byte (3 bytes cada) o
// cabeçalho sozinho passa dos 2000 bytes e o orçamento fica NEGATIVO. Sem o
// guard, codeBlockMax faz `s[len(s)-max:]` com max negativo e o índice passa
// do fim da string: PANIC dentro do handler de interação, numa goroutine do
// discordgo que ninguém recupera — o bot inteiro cai. O comportamento correto
// é o de antes desta mudança: publicar só o erro, sem a saída parcial.
func TestHandleModalSaidaParcialComErroMultibyteNaoDerrubaOHandler(t *testing.T) {
	causa := strings.Repeat("日", 1200) + " CAUSA-NO-FIM"
	parcial := strings.Repeat("linha de saida parcial do exec antes do stream quebrar no meio\n", 80)

	rt := &recordingTransport{}
	b := novoBotStreamQuebrado(t, rt, parcial, causa)

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("erro multi-byte longo estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "CAUSA-NO-FIM") {
		t.Fatalf("resposta efêmera não preservou a causa no fim do erro: %.400q", content)
	}
}

// TestHandleModalSaidaParcialNaoMudaAAuditoria é a contraprova de cor: o
// `result` da auditoria tem que continuar começando com "⚠️ erro:", porque a
// regra de cor invertida do audit() pinta de verde só o prefixo ✅ — publicar
// a saída parcial não pode transformar um erro em sucesso no canal durável.
func TestHandleModalSaidaParcialNaoMudaAAuditoria(t *testing.T) {
	rt := &recordingTransport{}
	b := novoBotStreamQuebrado(t, rt, "PARCIAL-MARCA\n", "FALHA-NO-MEIO")

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	resultado := strings.TrimPrefix(auditResultado(t, rt), "```\n")
	if !strings.HasPrefix(resultado, "⚠️ erro:") {
		t.Fatalf("auditoria não começou com o prefixo de erro esperado: %q", resultado)
	}
	// A saída parcial vai SÓ para a resposta efêmera do owner. A auditoria é
	// um canal: stdout de container não pode pousar lá por este ramo. Sem esta
	// asserção, `result = "⚠️ erro: " + emsg + out` passava verde (mutação M5
	// da drenagem de 12/11/2026) — o prefixo acima não distingue.
	if strings.Contains(resultado, "PARCIAL-MARCA") {
		t.Fatalf("a saída parcial do container vazou para a auditoria: %q", resultado)
	}
	// Contraprova positiva: asserção de AUSÊNCIA sozinha absorve mutação. A
	// mesma marca TEM de estar na resposta efêmera, senão "ausente na
	// auditoria" seria verdade só porque a saída nunca existiu neste cenário.
	if efemero := efemeroConteudo(t, rt); !strings.Contains(efemero, "PARCIAL-MARCA") {
		t.Fatalf("contraprova: a saída parcial deveria estar na resposta efêmera: %q", efemero)
	}
}
