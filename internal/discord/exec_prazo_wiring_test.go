package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do prazo real do /exec (ver
// internal/dockerx/exec_exitcode_test.go para a prova de Client.Exec
// isolado): sem o corte em ops.go:265-268, handleModal ficaria preso até o
// stream do comando fechar sozinho — nunca, no caso de ping/tail -f/top -b.

// fakeExecHostPendurado é fakeExecHostSaida (exec_audit_wiring_test.go) com o
// /start travado: escreve um frame parcial e NUNCA fecha o stream por conta
// própria. release fecha num Cleanup registrado DEPOIS do srv.Close — como
// Cleanup roda em ordem LIFO, ele roda ANTES, e srv.Close nunca trava
// esperando o handler sob mutação.
func fakeExecHostPendurado(t *testing.T) *dockerx.Client {
	t.Helper()
	release := make(chan struct{})

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
			_, _ = sw.Write([]byte("parcial\n"))
			_ = buf.Flush()

			gone := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, conn)
				close(gone)
			}()
			select {
			case <-gone:
			case <-release:
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("main", "Main")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// TestHandleModalExecPenduradoRespondeNoPrazo prova a fiação de ponta a
// ponta: um /exec cujo stream nunca fecha por conta própria (ping, tail -f)
// tem a resposta efêmera e a auditoria publicadas em torno do execTimeout
// configurado — não pendurado até o token da interação expirar em 15 min.
func TestHandleModalExecPenduradoRespondeNoPrazo(t *testing.T) {
	old := execTimeout
	execTimeout = 300 * time.Millisecond
	t.Cleanup(func() { execTimeout = old })

	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{fakeExecHostPendurado(t)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}

	done := make(chan struct{})
	go func() {
		b.handleModal(execModalInteraction("ping 1.1.1.1"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleModal ignorou o prazo e ficou pendurado")
	}
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if !strings.Contains(content, "parcial") {
		t.Fatalf("resposta efêmera não trouxe a saída parcial recebida antes do corte: %q", content)
	}
	if !strings.Contains(content, "interrompido") {
		t.Fatalf("resposta efêmera não avisa que o exec foi interrompido no prazo: %q", content)
	}

	result := strings.TrimPrefix(auditResultado(t, rt), "```\n")
	if !strings.HasPrefix(result, "⚠️") {
		t.Fatalf("auditoria não marcou o exec interrompido com ⚠️: %q", result)
	}
	if strings.Contains(result, "✅") {
		t.Fatalf("auditoria não pode marcar sucesso num exec interrompido pelo prazo: %q", result)
	}
}
