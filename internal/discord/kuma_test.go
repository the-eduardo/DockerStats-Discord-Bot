package discord

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// resetKumaState garante que cada teste comece do estado "saudavel", sem
// vazar kumaFailing de um caso pro outro (variavel de pacote compartilhada).
func resetKumaState(t *testing.T) {
	t.Helper()
	kumaMu.Lock()
	kumaFailing = false
	kumaMu.Unlock()
}

// TestPushKumaLogaTransicao prova o status HTTP (nao so erro de transporte)
// e o anti-spam por transicao: 404,404,200 tem que gerar exatamente 1 linha
// de falha + 1 linha de recuperacao, nunca uma por chamada.
func TestPushKumaLogaTransicao(t *testing.T) {
	resetKumaState(t)

	var status atomic.Int32
	status.Store(http.StatusNotFound)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"ok":false,"msg":"Monitor not found or not active."}`))
	}))
	defer srv.Close()
	t.Setenv("KUMA_PUSH_URL", srv.URL)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	// 1) primeira chamada com 404: precisa logar a falha, citando o status.
	pushKuma()
	if got := buf.String(); !containsAll(got, "push do Kuma falhou", "404") {
		t.Fatalf("esperava log de falha com status 404, recebi: %q", got)
	}

	// 2) segunda chamada AINDA 404: nao pode logar de novo (mesma transicao).
	buf.Reset()
	pushKuma()
	if got := buf.String(); got != "" {
		t.Fatalf("segunda falha consecutiva nao deveria logar (anti-spam por transicao), recebi: %q", got)
	}

	// 3) volta a 200: precisa logar a recuperacao, e SO a recuperacao.
	status.Store(http.StatusOK)
	buf.Reset()
	pushKuma()
	if got := buf.String(); !containsAll(got, "normalizado") {
		t.Fatalf("esperava log de recuperacao, recebi: %q", got)
	}
}

// TestPushKumaSucessoNaoLoga e a contraprova de que o caso 1 pega o STATUS,
// nao dispara um log incondicional: 200 de primeira tem que ficar em silencio.
func TestPushKumaSucessoNaoLoga(t *testing.T) {
	resetKumaState(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	t.Setenv("KUMA_PUSH_URL", srv.URL)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	pushKuma()

	if got := buf.String(); got != "" {
		t.Fatalf("push com 200 nao deveria logar nada, recebi: %q", got)
	}
}

// TestDashboardLoopEmpurraHeartbeat prova a FIACAO: render() OK dentro do
// loop de verdade (nao a funcao pushKuma isolada) precisa disparar o
// heartbeat do Kuma. Remover a chamada em dashboard.go:76 deixaria este
// teste passar com hits==0.
//
// Precisa de pelo menos um host que responda List() com sucesso: desde que
// render() passou a exigir `vivo` (heartbeat cego ao Docker inacessível,
// 05/09/2026), um Bot sem nenhum host (hosts == nil, como era antes deste
// ajuste) nunca fica "vivo" e o push nunca sai -- não é o que este teste quer
// provar. listOneContainerHost vem de render_collect_wiring_test.go, mesmo
// pacote.
func TestDashboardLoopEmpurraHeartbeat(t *testing.T) {
	resetKumaState(t)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("KUMA_PUSH_URL", srv.URL)

	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)
	d.bot.cfg = &config.Config{RefreshInterval: 20 * time.Millisecond, DiskPath: "/"}
	d.bot.hosts = []*dockerx.Client{listOneContainerHost(t)}

	go d.loop()
	defer d.stop()

	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := hits.Load(); got < 1 {
		t.Fatalf("esperava pelo menos 1 push de heartbeat apos render OK, recebi %d", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestPushKumaNaoVazaOTokenNoLog: KUMA_PUSH_URL embute o push token no path
// (/api/push/<token>). Num erro de transporte, err.Error() de *url.Error inclui
// a URL inteira — e como kumaState loga a cada TRANSICAO (nao mais uma vez por
// processo), um Kuma instavel publicaria o token no Loki a cada flap.
// Achado do painel AppSec na drenagem de 25/08/2026.
func TestPushKumaNaoVazaOTokenNoLog(t *testing.T) {
	const token = "tokenSuperSecreto123"
	// porta 1 em 127.0.0.1 recusa conexao de imediato: erro de TRANSPORTE
	// deterministico, sem DNS e sem espera.
	t.Setenv("KUMA_PUSH_URL", "http://127.0.0.1:1/api/push/"+token)

	kumaMu.Lock()
	kumaFailing = false // garante que ESTA chamada e' uma transicao ok->falha
	kumaMu.Unlock()

	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	pushKuma()

	saida := buf.String()
	if saida == "" {
		t.Fatal("esperava log da transicao ok->falha, nao veio nada")
	}
	if strings.Contains(saida, token) {
		t.Errorf("o push token vazou no log: %q", saida)
	}
	if !strings.Contains(saida, "connect") && !strings.Contains(saida, "refused") {
		t.Errorf("a causa do erro se perdeu no log: %q", saida)
	}
}
