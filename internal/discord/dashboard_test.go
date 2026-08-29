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
	editCount atomic.Int32

	mu       sync.Mutex
	lastBody []byte // corpo do último POST de criação -- usado por quem precisa inspecionar o embed/componentes publicados
}

func (t *fakeDiscordTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body := `{"id":"msg-1","channel_id":"123"}`
	switch {
	case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/messages"):
		if t.postDelay > 0 {
			time.Sleep(t.postDelay)
		}
		if req.Body != nil {
			b, _ := io.ReadAll(req.Body)
			req.Body.Close()
			t.mu.Lock()
			t.lastBody = b
			t.mu.Unlock()
		}
		t.postCount.Add(1)
	case req.Method == http.MethodPatch && strings.Contains(req.URL.Path, "/messages/"):
		t.editCount.Add(1)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (t *fakeDiscordTransport) lastPostBody() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastBody
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

// waitForRenders espera até que postCount+editCount alcance want, ou falha no
// timeout. Usado pelos testes de refreshAfterAction, onde o render acontece
// numa goroutine e não há como prever se vai ser criação (POST) ou edição
// (PATCH) da mensagem-painel.
func waitForRenders(t *testing.T, transport *fakeDiscordTransport, want int32) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if got := transport.postCount.Load() + transport.editCount.Load(); got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("esperava pelo menos %d render(s) publicado(s), veio %d", want, transport.postCount.Load()+transport.editCount.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// TestRefreshAfterActionNaoDesisteComRenderEmVoo prova o comportamento
// oposto ao de refreshNow: com uma ação de estado já aplicada (start/stop/...),
// refreshAfterAction() NÃO pode desistir só porque há um render em voo -- esse
// render pode ter coletado os dados ANTES da ação terminar, e publicaria
// estado velho. Aqui simulamos o render em voo segurando renderMu na mão; ao
// soltar, o refresh enfileirado tem que publicar.
func TestRefreshAfterActionNaoDesisteComRenderEmVoo(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)

	d.renderMu.Lock() // simula um render já em andamento
	d.refreshAfterAction()
	time.Sleep(30 * time.Millisecond) // dá tempo do refreshAfterAction tentar e enfileirar
	d.renderMu.Unlock()

	waitForRenders(t, transport, 1)
}

// TestRefreshAfterActionColescaRajada prova o limite da fila: N chamadas em
// rajada, todas com o render em voo, geram no máximo 1 render extra -- não N.
// É o que impede que N ações rápidas (toques repetidos) atrasem o push do
// Kuma no tick seguinte por causa de uma fila de render inchada.
func TestRefreshAfterActionColescaRajada(t *testing.T) {
	transport := &fakeDiscordTransport{postDelay: 20 * time.Millisecond}
	d := newTestDashboard(t, transport)

	d.renderMu.Lock() // simula um render já em andamento
	for i := 0; i < 5; i++ {
		d.refreshAfterAction()
	}
	time.Sleep(30 * time.Millisecond)
	d.renderMu.Unlock()

	waitForRenders(t, transport, 1)
	time.Sleep(150 * time.Millisecond) // sobra de tempo para um 2º render indevido aparecer, se houver

	if got := transport.postCount.Load(); got != 1 {
		t.Fatalf("5 refreshAfterAction em rajada deveriam coalescer em 1 render (POST de criação), saíram %d", got)
	}
}

// TestRefreshAfterActionDuasRodadasSequenciais prova que refreshPending é
// liberado DENTRO de render() (não antes): uma segunda ação, chegada depois
// que o primeiro render termina, tem que enfileirar o seu próprio render --
// não ficar presa para sempre porque a primeira "esqueceu" de liberar a fila.
func TestRefreshAfterActionDuasRodadasSequenciais(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)

	d.refreshAfterAction()
	waitForRenders(t, transport, 1)

	d.refreshAfterAction()
	waitForRenders(t, transport, 2)
}
