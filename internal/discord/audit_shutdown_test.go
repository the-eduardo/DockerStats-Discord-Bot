package discord

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// TestStopDrenaAuditoriaAntesDeRemoverComandos prova a ORDEM do shutdown: o
// POST de auditoria tem que sair ANTES de qualquer DELETE de slash command.
// unregisterCommands faz 9 DELETE REST sequenciais e o discordgo retenta
// 5xx/dorme no 429 — duracao NAO limitada — entao rodar isso antes da
// auditoria arrisca comer o orcamento de 10s do SIGTERM->SIGKILL e perder o
// registro. Achado da triagem de 31/08/2026.
func TestStopDrenaAuditoriaAntesDeRemoverComandos(t *testing.T) {
	ordem := &ordemTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: ordem}
	session.State.User = &discordgo.User{ID: "app"}

	b := &Bot{session: session, cfg: &config.Config{AuditChannelID: "999"}}
	b.dashboard = newDashboard(b)
	b.registered = make([]*discordgo.ApplicationCommand, 0, 9)
	for i := 0; i < 9; i++ {
		b.registered = append(b.registered, &discordgo.ApplicationCommand{ID: "c" + string(rune('1'+i))})
	}

	b.auditRefusal(auditEntry{actor: "eduardo", action: "stop", host: "main", target: "web"})

	b.Stop()

	seq := ordem.sequence()
	if len(seq) == 0 {
		t.Fatal("nenhuma chamada registrada pelo transport")
	}
	if seq[0] != "POST /api/v9/channels/999/messages" {
		t.Fatalf("primeira chamada devia ser o POST de auditoria, veio: %q (sequencia completa: %v)", seq[0], seq)
	}
	if len(seq) < 2 || !strings.HasPrefix(seq[1], "DELETE") {
		t.Fatalf("esperava os DELETE de comando logo apos o POST de auditoria; sequencia: %v", seq)
	}
}

// ordemTransport grava, em ordem, "METODO PATH" de cada chamada REST feita
// pela sessao — usado para provar que a auditoria drena ANTES dos DELETE de
// slash command. Nome distinto de blockingTransport/recordingTransport de
// proposito: colisao de helper entre arquivos de teste ja travou branch
// anterior (25/08/2026).
type ordemTransport struct {
	mu    sync.Mutex
	calls []string
}

func (o *ordemTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	o.mu.Lock()
	o.calls = append(o.calls, req.Method+" "+req.URL.Path)
	o.mu.Unlock()
	if req.Method == http.MethodDelete {
		time.Sleep(50 * time.Millisecond)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func (o *ordemTransport) sequence() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.calls))
	copy(out, o.calls)
	return out
}

// audit() e assincrono de proposito. Sem contador de trabalho em voo, Stop()
// fecha a sessao e o main retorna enquanto ainda ha POST pendente — e o
// registro se perde no SIGTERM de um deploy/restart/OOM, ou seja, no evento em
// que a auditoria mais importa. Achado do painel Dev Senior em 25/08/2026.

func TestEsperaAuditoriaBloqueiaAteTerminar(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	if esperaAuditoria(&wg, 30*time.Millisecond) {
		t.Fatal("com trabalho em voo, esperaAuditoria devia estourar o prazo e devolver false")
	}
	wg.Done()
	if !esperaAuditoria(&wg, time.Second) {
		t.Fatal("sem trabalho pendente, esperaAuditoria devia devolver true de imediato")
	}
}

// Fiacao: prova que audit() de fato REGISTRA no contador — sem isso a espera do
// Stop() existe mas nunca tem o que esperar (funcao pura correta, call site
// morto: o buraco que ja se repetiu 3x neste acervo).
func TestAuditRegistraTrabalhoEmVooNoContador(t *testing.T) {
	libera := make(chan struct{})
	rt := &blockingTransport{libera: libera}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session, cfg: &config.Config{AuditChannelID: "999"}}

	b.audit(auditEntry{actor: "eduardo", action: "stop", host: "main", target: "web", result: "✅ parado"})

	// O POST esta preso no transport: a espera TEM que estourar o prazo.
	if esperaAuditoria(&b.auditWG, 50*time.Millisecond) {
		t.Fatal("CONTADOR VAZIO: audit() nao registrou o POST em voo, entao Stop() nao esperaria por ele")
	}
	close(libera) // solta o POST
	if !esperaAuditoria(&b.auditWG, 2*time.Second) {
		t.Fatal("depois de liberado, o contador devia zerar")
	}
}

// blockingTransport segura a requisicao ate `libera` fechar — simula o POST de
// auditoria ainda em voo no instante do shutdown.
type blockingTransport struct{ libera chan struct{} }

func (t *blockingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	<-t.libera
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}
