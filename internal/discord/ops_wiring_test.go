package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO, não as funções puras: tailBytes tem testes
// próprios, mas remover a chamada `out = tailBytes(out, maxAttach)` de dentro
// do cmdLogs deixava a suíte inteira verde (verificado por mutação em
// 15/08/2026) — e produção voltaria a estourar o teto de upload. O padrão já
// repetiu três vezes no acervo do triador: função pura bem testada, call site
// sem teste nenhum.

// recordingTransport intercepta TODA chamada HTTP do discordgo: nada sai para
// a rede, e cada corpo enviado fica guardado para inspeção.
type recordingTransport struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	rt.mu.Lock()
	rt.bodies = append(rt.bodies, body)
	rt.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func (rt *recordingTransport) all() []byte {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var out []byte
	for _, b := range rt.bodies {
		out = append(out, b...)
	}
	return out
}

// fakeDockerHost sobe um stub mínimo da API do Docker (ping, inspect com
// Tty=true para o log vir cru, logs com o payload dado) e devolve um
// *dockerx.Client apontado para ele via DOCKER_HOST.
func fakeDockerHost(t *testing.T, logPayload string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "deadbeef",
				"Config": map[string]any{"Tty": true},
				"State":  map[string]any{"Status": "running"},
			})
		case strings.HasSuffix(r.URL.Path, "/logs"):
			_, _ = io.WriteString(w, logPayload)
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

func newWiringBot(t *testing.T, logPayload string) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		hosts:   []*dockerx.Client{fakeDockerHost(t, logPayload)},
		session: session,
	}, rt
}

func logsInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "logs",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: "web"},
			},
		},
	}}
}

func TestCmdLogsAppliesAttachmentCapBeforeUpload(t *testing.T) {
	// Log maior que maxAttach: o anexo enviado TEM que sair truncado, com o
	// marcador do tailBytes no começo do arquivo.
	line := strings.Repeat("linha de log bem comprida para encher o anexo ", 2) + "\n"
	payload := strings.Repeat(line, (maxAttach/len(line))+2_000) // ~7.2 MiB + folga

	b, rt := newWiringBot(t, payload)
	b.cmdLogs(logsInteraction())

	sent := rt.all()
	if len(sent) == 0 {
		t.Fatal("nenhuma chamada chegou ao Discord fake")
	}
	if !strings.Contains(string(sent), "…(truncado") {
		t.Fatal("anexo subiu SEM o marcador de truncamento: cmdLogs não passou o log por tailBytes antes do upload")
	}
	// Corpo total (multipart + json do defer) tem que ficar perto do teto, não
	// no tamanho original do log.
	if len(sent) > maxAttach+4096 {
		t.Fatalf("upload com %d bytes, quer <= %d: o teto não foi aplicado", len(sent), maxAttach+4096)
	}
}

func TestCmdLogsSendsSmallLogInlineWithoutTruncation(t *testing.T) {
	// Contraprova: log pequeno vai inline, sem marcador — garante que o teste
	// acima detecta o teto, não um truncamento incondicional.
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cmdLogs(logsInteraction())

	sent := string(rt.all())
	if strings.Contains(sent, "…(truncado") {
		t.Fatal("log pequeno saiu truncado")
	}
	if !strings.Contains(sent, "só uma linha") {
		t.Fatal("log pequeno não chegou na resposta")
	}
}
