package discord

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// handleSelect chamava host.State e, para QUALQUER erro (SSH caído, proxy
// fora, timeout), respondia "não encontrado" — diagnóstico ativamente errado.
// Este arquivo testa a FIAÇÃO: o handler real, contra um stub que devolve 500
// (falha de transporte, não 404), não pode mostrar a mensagem de "não
// encontrado".

// selectFakeHostFalhaTransporte sobe um stub cujo inspect sempre devolve 500,
// simulando socket-proxy ou host remoto fora do ar.
func selectFakeHostFalhaTransporte(t *testing.T) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"proxy fora do ar"}`))
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

func TestHandleSelectNaoDizNaoEncontradoQuandoFalhaETransporte(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	host := selectFakeHostFalhaTransporte(t)
	b := &Bot{hosts: []*dockerx.Client{host}, session: session}

	b.handleSelect(selectInteraction("main:web"))

	full := string(rt.all())
	if strings.Contains(full, "não encontrado") {
		t.Fatalf("falha de transporte (500) foi relatada como container ausente: %s", full)
	}
	if !strings.Contains(full, "Falha ao consultar") {
		t.Fatalf("resposta não contém a mensagem de falha de transporte esperada: %s", full)
	}
}
