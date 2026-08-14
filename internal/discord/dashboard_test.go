package discord

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// fakeDiscordTransport intercepta as chamadas REST do discordgo sem tocar rede.
// Conta quantos POST (criação de mensagem) realmente saíram -- é o sinal que
// prova (ou derruba) o dedup de render().
type fakeDiscordTransport struct {
	postDelay time.Duration
	postCount atomic.Int32
}

func (t *fakeDiscordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := `{"id":"msg-1","channel_id":"123"}`
	switch {
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/messages"):
		if t.postDelay > 0 {
			time.Sleep(t.postDelay)
		}
		t.postCount.Add(1)
	case req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/messages/"):
		// edit: ok, sem contar
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func newTestDashboard(t *testing.T, transport *fakeDiscordTransport) *Dashboard {
	t.Helper()
	session, err := discordgo.New("Bot faketoken")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: transport}

	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	bot := &Bot{session: session, store: st} // hosts=nil: dashboardEmbeds/buildDashboardComponents ficam vazios, sem depender de Docker
	d := newDashboard(bot)
	d.channelID = "123"
	return d
}

// TestRenderConcorrenteNaoDuplicaPainel prova o motivo de existir o renderMu:
// duas chamadas a render() disparadas ao mesmo tempo com messageID == "" caem
// as duas no ramo de criação sem a trava, e cada uma publica uma mensagem nova.
// Com a trava, só a primeira cria; a segunda espera e edita a que já existe.
func TestRenderConcorrenteNaoDuplicaPainel(t *testing.T) {
	transport := &fakeDiscordTransport{postDelay: 50 * time.Millisecond}
	d := newTestDashboard(t, transport)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.render()
		}()
	}
	wg.Wait()

	if got := transport.postCount.Load(); got != 1 {
		t.Fatalf("esperava exatamente 1 POST de criação de painel, recebi %d (painel duplicado)", got)
	}
}

// TestRefreshNowDesisteComRenderEmVoo prova que cliques repetidos no
// "Atualizar agora" não empilham N renders: com um render já em voo (aqui
// simulado segurando renderMu na mão, sem depender de timing de HTTP), o
// refreshNow() desiste via TryLock em vez de chamar render().
func TestRefreshNowDesisteComRenderEmVoo(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)

	d.renderMu.Lock() // simula um render já em andamento
	d.refreshNow()
	time.Sleep(30 * time.Millisecond) // dá tempo da goroutine do refreshNow tentar e desistir
	d.renderMu.Unlock()

	if got := transport.postCount.Load(); got != 0 {
		t.Fatalf("refreshNow deveria ter desistido (renderMu já em uso), mas chamou render mesmo assim: %d POST(s)", got)
	}
}
