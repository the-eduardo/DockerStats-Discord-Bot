package discord

import (
	"bytes"
	"io"
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
	defer stopAndJoinLoop(t, d)

	deadline := time.Now().Add(2 * time.Second)
	for hits.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := hits.Load(); got < 1 {
		t.Fatalf("esperava pelo menos 1 push de heartbeat apos render OK, recebi %d", got)
	}
}

// TestDashboardLoopNaoEmpurraHeartbeatSemProvaDeVida é o negativo que faltava
// no nível do loop(): TestDashboardLoopEmpurraHeartbeat prova o lado positivo
// (render OK -> push), e render_collect_wiring_test.go prova o lado negativo
// mas só na função render() isolada (TestRenderComHostFalhandoPublicaPlaceholder
// e TestRenderEditandoComTodosHostsCegosSuprimeHeartbeat). Nenhum teste
// anterior passava pelo loop() de verdade asserindo AUSÊNCIA de push --
// `if d.render() { pushKuma() }` (dashboard.go) poderia virar incondicional
// (heartbeat sempre sai) ou o bool de render() poderia parar de refletir
// "Discord aceitou a chamada" sem que a suíte acusasse.
func TestDashboardLoopNaoEmpurraHeartbeatSemProvaDeVida(t *testing.T) {
	casos := []struct {
		nome    string
		prepara func(d *Dashboard, tr *fakeDiscordTransport)
	}{
		{"todos os hosts cegos", func(d *Dashboard, tr *fakeDiscordTransport) {
			d.bot.hosts = []*dockerx.Client{listFailingHost(t)}
		}},
		{"Discord recusa a edicao", func(d *Dashboard, tr *fakeDiscordTransport) {
			d.bot.hosts = []*dockerx.Client{listOneContainerHost(t)}
			tr.editStatus = 503
		}},
		{"Discord recusa a criacao", func(d *Dashboard, tr *fakeDiscordTransport) {
			d.bot.hosts = []*dockerx.Client{listOneContainerHost(t)}
			tr.createStatus = 503
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			resetKumaState(t)

			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()
			t.Setenv("KUMA_PUSH_URL", srv.URL)
			log.SetOutput(io.Discard)
			defer log.SetOutput(os.Stderr)

			tr := &fakeDiscordTransport{}
			d := newTestDashboard(t, tr)
			d.bot.cfg = &config.Config{RefreshInterval: 20 * time.Millisecond, DiskPath: "/"}
			c.prepara(d, tr)

			go d.loop()
			deadline := time.Now().Add(5 * time.Second)
			for tr.postCount.Load()+tr.editCount.Load() < 4 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			stopAndJoinLoop(t, d)
			// controle positivo: sem ticks, hits==0 nao provaria nada
			if n := tr.postCount.Load() + tr.editCount.Load(); n < 4 {
				t.Fatalf("loop nao tickou (%d chamadas ao Discord): teste vazio", n)
			}
			if got := hits.Load(); got != 0 {
				t.Fatalf("heartbeat saiu %d vez(es) sem prova de vida", got)
			}
		})
	}
}

// stopAndJoinLoop para o loop() e ESPERA a goroutine de fato retornar (via
// d.loopDone) antes de devolver o controle ao teste. d.stop() por si só só
// fecha o canal done -- se um render() já estiver em voo (a chamada HTTP ao
// fake do Docker leva ~600ms-1s neste pacote), a goroutine do loop() sobrevive
// ao retorno do teste e pode terminar essa chamada (e, se vivo, empurrar um
// heartbeat) DEPOIS que o teste seguinte já trocou o KUMA_PUSH_URL do
// processo para o próprio servidor -- pushKuma() lê o env em cada chamada,
// então o heartbeat atrasado da goroutine órfã aterrissa no contador do
// próximo teste. Sem este join, isso produzia um teste flaky (achado ao
// rodar TestDashboardLoopNaoEmpurraHeartbeatSemProvaDeVida logo depois de
// TestDashboardLoopEmpurraHeartbeat na mesma execução).
func stopAndJoinLoop(t *testing.T, d *Dashboard) {
	t.Helper()
	d.stop()
	select {
	case <-d.loopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("loop() nao retornou apos stop(): goroutine presa, risco de vazar para o proximo teste")
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
