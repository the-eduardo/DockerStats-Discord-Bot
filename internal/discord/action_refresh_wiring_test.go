package discord

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// Este arquivo testa a FIAÇÃO do refresh pós-ação: dashboard_test.go prova que
// Dashboard.refreshAfterAction() não desiste com render em voo, mas isso não
// garante que handleAction/handleConfirm de fato CHAMAM esse método -- o
// mesmo buraco (função nova bem testada, call site sem teste) já se repetiu
// três vezes neste acervo (verificado por mutação em 15/08/2026).

// dashboardPostCountingTransport conta chamadas de criação/edição da
// mensagem-painel no canal do dashboard, além de gravar todo corpo (igual
// recordingTransport) para os handlers que respondem a interação.
type dashboardPostCountingTransport struct {
	recordingTransport
	dashboardChannelID string
	dashboardRenders   atomic.Int32
}

func (t *dashboardPostCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	prefix := "/channels/" + t.dashboardChannelID + "/messages"
	if strings.Contains(req.URL.Path, prefix) &&
		(req.Method == http.MethodPost || req.Method == http.MethodPatch) {
		t.dashboardRenders.Add(1)
	}
	return t.recordingTransport.RoundTrip(req)
}

func newActionWiringBot(t *testing.T) (*Bot, *dashboardPostCountingTransport) {
	t.Helper()
	rt := &dashboardPostCountingTransport{dashboardChannelID: "555"}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	b := &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{fakeDockerHost(t, "")},
		session: session,
		limiter: newRateLimiter(8, 0.5),
	}
	// O ramo default de handleAction termina em refreshAfterAction/refreshNow:
	// sem dashboard setado, ele panica (dashboard nil). Ver STATE do projeto.
	b.dashboard = newDashboard(b)
	b.dashboard.channelID = rt.dashboardChannelID
	b.confirms = newConfirmManager(b)

	// Fecha o vazamento de goroutine de render() (Add(1)/Done() em
	// dashboard.go): sem esperar aqui, a goroutine disparada por
	// refreshAfterAction()/refreshNow() pode seguir gravando no store DEPOIS
	// que o teste termina e t.TempDir() já começou a apagar o diretório --
	// era exatamente esse race que produzia o flake "TempDir RemoveAll
	// cleanup: unlinkat ... directory not empty" (drenagem de 01/09/2026: não
	// reproduz mais em 285 execuções, mas o vazamento estrutural continua).
	// Registrado ANTES de store.New(t.TempDir()) DE PROPÓSITO: t.Cleanup é
	// LIFO e o cleanup de remoção do TempDir é registrado dentro da própria
	// chamada t.TempDir() logo abaixo -- a ordem de registro no código aqui
	// importa.
	t.Cleanup(func() { b.dashboard.renderWG.Wait() })

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	b.store = st

	return b, rt
}

func actionInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func waitForDashboardRenders(t *testing.T, rt *dashboardPostCountingTransport, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if rt.dashboardRenders.Load() >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("esperava pelo menos %d render(s) do painel, veio %d", want, rt.dashboardRenders.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestHandleActionStartRefreshSobreviveARenderEmVoo prova a fiação: o ramo
// default de handleAction (start/pause/unpause) chama refreshAfterAction, não
// refreshNow. A diferença só aparece com um render já em voo -- por isso o
// teste segura renderMu ANTES de disparar a ação, exatamente como pediu a
// proposta de 24/08. Com refreshNow (a versão antiga), o TryLock falharia e o
// refresh seria perdido para sempre: o painel nunca seria republicado, mesmo
// depois de soltar a trava.
// exigeAcaoExecutada e um CONTROLE POSITIVO de caminho, nao uma assercao de
// resultado: prova que o teste passou pelo ramo que EXECUTA a acao, e nao pelo
// de recusa por rate limit. Sem ele, trocar o limiter do construtor por um que
// recusa tudo deixaria os dois testes deste arquivo VERDES — refreshAfterAction
// roda incondicionalmente depois da recusa (components.go:280 e :359), entao o
// render esperado aparece de qualquer jeito e nenhuma acao Docker executa.
// Achado do QA no comite da drenagem de 25/08/2026: a prova por mutacao NAO
// pega essa classe (com o limiter errado E a mutacao, os testes continuam
// falhando "corretamente"); so um controle positivo pega.
func exigeAcaoExecutada(t *testing.T, rt *dashboardPostCountingTransport, marca string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		rt.mu.Lock()
		var junto []byte
		for _, b := range rt.bodies {
			junto = append(junto, b...)
		}
		rt.mu.Unlock()
		if strings.Contains(string(junto), marca) {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("a acao nao chegou a executar (marca %q ausente): o teste esta exercitando o ramo de RECUSA, nao o de acao", marca)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestHandleActionStartRefreshSobreviveARenderEmVoo(t *testing.T) {
	b, rt := newActionWiringBot(t)

	b.dashboard.renderMu.Lock() // simula um render em voo que já coletou dados
	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	// A goroutine que refreshNow()/refreshAfterAction() dispara é assíncrona:
	// sem esperar aqui, o Unlock abaixo pode acontecer ANTES do TryLock/Swap
	// dela rodar, e aí o teste passaria mesmo com refreshNow() (falso positivo
	// -- verificado por mutação: sem este sleep, o mutante refreshNow passava).
	time.Sleep(30 * time.Millisecond)
	b.dashboard.renderMu.Unlock()

	waitForDashboardRenders(t, rt, 1)
	exigeAcaoExecutada(t, rt, "iniciado")
}

// TestHandleConfirmStopRefreshSobreviveARenderEmVoo é o espelho do teste
// acima para o caminho pós-confirmação (stop/restart), a outra metade da
// proposta de 24/08.
func TestHandleConfirmStopRefreshSobreviveARenderEmVoo(t *testing.T) {
	b, rt := newActionWiringBot(t)
	token := b.confirms.add("stop", "main", "web", actionInteraction("act:stop:main:web").Interaction)

	b.dashboard.renderMu.Lock()
	b.handleConfirm(actionInteraction("cfm:ok:"+token), "cfm:ok:"+token)
	time.Sleep(30 * time.Millisecond) // mesmo motivo do teste acima
	b.dashboard.renderMu.Unlock()

	waitForDashboardRenders(t, rt, 1)
}


// TestRefreshAfterActionRegistraTrabalhoEmVooNoRenderWG prova a fiacao do
// fecho do vazamento de goroutine: refreshAfterAction() tem que registrar a
// goroutine de render() no renderWG (Add(1)) ANTES de disparar o "go" -- sem
// isso, o Wait() usado no cleanup de newActionWiringBot (e por qualquer
// chamador em producao que precise esperar, via esperaAuditoria) nao espera
// NADA: funcao pura correta, contador nunca incrementado, o mesmo buraco que
// ja se repetiu 3x neste acervo (verificado por mutacao em 15/08/2026).
//
// Usa blockingTransport (mesmo helper de audit_shutdown_test.go) para
// prender a chamada HTTP do render() em voo, e esperaAuditoria (helper
// generico de bot.go, aceita qualquer *sync.WaitGroup) para provar por
// PRAZO -- deterministico, sem sleep de adivinhacao e sem depender da
// corrida do TempDir (que nao reproduz mais e nao serve como prova, so como
// sintoma historico):
//   - com o POST preso, o contador TEM que estar > 0 (esperaAuditoria com
//     prazo curto tem que estourar, ou seja, devolver false);
//   - depois de liberado, o contador TEM que zerar (esperaAuditoria com
//     prazo generoso devolve true).
//
// Mutacao 1 (apagar o Add(1) de refreshAfterAction): o contador fica sempre
// em 0, a primeira esperaAuditoria devolve true de imediato -- t.Fatal
// dispara na hora, sem esperar prazo nenhum.
func TestRefreshAfterActionRegistraTrabalhoEmVooNoRenderWG(t *testing.T) {
	libera := make(chan struct{})
	rt := &blockingTransport{libera: libera}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	b := &Bot{cfg: &config.Config{}, session: session}
	b.dashboard = newDashboard(b)
	b.dashboard.channelID = "555"

	b.dashboard.refreshAfterAction()

	// O POST esta preso no transport: o render() em voo tem que aparecer no
	// contador.
	if esperaAuditoria(&b.dashboard.renderWG, 50*time.Millisecond) {
		t.Fatal("CONTADOR VAZIO: refreshAfterAction() nao registrou a goroutine de render() em voo no renderWG")
	}
	close(libera) // solta o render
	if !esperaAuditoria(&b.dashboard.renderWG, 2*time.Second) {
		t.Fatal("depois de liberado, o renderWG devia zerar")
	}
}

// TestRefreshNowRegistraTrabalhoEmVooNoRenderWG e o espelho do teste acima
// para o outro call site (refreshNow(), ligado ao botao Atualizar). Cobrir so
// um dos dois "go d.render()" deixaria o outro vazando -- ver CONTEXTO da
// proposta P4 da drenagem de 01/09/2026.
func TestRefreshNowRegistraTrabalhoEmVooNoRenderWG(t *testing.T) {
	libera := make(chan struct{})
	rt := &blockingTransport{libera: libera}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	b := &Bot{cfg: &config.Config{}, session: session}
	b.dashboard = newDashboard(b)
	b.dashboard.channelID = "555"

	b.dashboard.refreshNow()

	if esperaAuditoria(&b.dashboard.renderWG, 50*time.Millisecond) {
		t.Fatal("CONTADOR VAZIO: refreshNow() nao registrou a goroutine de render() em voo no renderWG")
	}
	close(libera)
	if !esperaAuditoria(&b.dashboard.renderWG, 2*time.Second) {
		t.Fatal("depois de liberado, o renderWG devia zerar")
	}
}
