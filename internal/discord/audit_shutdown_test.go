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

var _ = strings.Contains
