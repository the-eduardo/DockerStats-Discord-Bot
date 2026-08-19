package discord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// handleSelect chamava host.State (rede: ContainerInspect via socket-proxy, no
// host remoto um `ssh` novo com ConnectTimeout=10s) ANTES do InteractionRespond
// inicial, que tem janela de 3s do Discord. Este arquivo testa a FIAÇÃO: o
// oráculo é causal (a ordem real das chamadas), não um sleep — o stub do
// Docker, ao responder o inspect, grava se o defer já tinha saído.

// selectFakeHost sobe um stub mínimo da API do Docker cujo handler de inspect
// registra, no instante em que é chamado, se o recordingTransport já recebeu
// algum corpo (ou seja, se a interação já foi respondida).
func selectFakeHost(t *testing.T, rt *recordingTransport) (*dockerx.Client, *bool) {
	t.Helper()
	deferJaSaiu := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			rt.mu.Lock()
			deferJaSaiu = len(rt.bodies) > 0
			rt.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "deadbeef",
				"Config": map[string]any{"Tty": true},
				"State":  map[string]any{"Status": "running"},
			})
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
	return h, &deferJaSaiu
}

func selectInteraction(value string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.MessageComponentInteractionData{
			CustomID: "select-container",
			Values:   []string{value},
		},
	}}
}

func TestHandleSelectDefereAntesDeChamarDocker(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	host, deferJaSaiu := selectFakeHost(t, rt)
	b := &Bot{hosts: []*dockerx.Client{host}, session: session}

	b.handleSelect(selectInteraction("main:web"))

	if !*deferJaSaiu {
		t.Fatal("handleSelect chamou o Docker (ContainerInspect) ANTES de deferir a interação")
	}

	sent := rt.bodies
	if len(sent) < 1 {
		t.Fatal("nenhuma chamada chegou ao Discord fake")
	}
	if !strings.Contains(string(sent[0]), `"type":5`) {
		t.Fatalf("1ª chamada ao Discord não foi o defer (type 5): %s", string(sent[0]))
	}

	full := string(rt.all())
	if strings.Contains(full, `"type":4`) {
		t.Fatal("handleSelect ainda respondeu com ChannelMessageWithSource (type 4) — resposta dupla à interação")
	}
	if !strings.Contains(full, "act:") {
		t.Fatal("os botões de ação não chegaram na edição da resposta")
	}
}
