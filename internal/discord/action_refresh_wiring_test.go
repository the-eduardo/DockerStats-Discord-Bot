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

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	b := &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{fakeDockerHost(t, "")},
		session: session,
		store:   st,
		limiter: newRateLimiter(8, 0.5),
	}
	// O ramo default de handleAction termina em refreshAfterAction/refreshNow:
	// sem dashboard setado, ele panica (dashboard nil). Ver STATE do projeto.
	b.dashboard = newDashboard(b)
	b.dashboard.channelID = rt.dashboardChannelID
	b.confirms = newConfirmManager(b)
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
