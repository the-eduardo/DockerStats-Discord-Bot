package discord

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// Este arquivo testa a FIAÇÃO do /dashboard (cmdDashboard -> moveTo -> render),
// não render() isolado (que já tem prova própria em render_collect_wiring_test.go):
// antes desta mudança, cmdDashboard respondia "✅ Painel fixado" mesmo quando o
// POST de criação da mensagem-painel era recusado pelo Discord (403, permissão
// faltando) -- moveTo() não devolvia nada, e a resposta efêmera não checava o
// resultado do render.

// panelCreateTransport intercepta o POST de criação da mensagem-painel
// (/channels/<alvo>/messages) e devolve 403 ou 200+id conforme o knob. Sem o
// knob de sucesso, TODO corpo (inclusive esse) sairia como "{}" via
// recordingTransport -- e até o caminho de SUCESSO se disfarçaria de falha,
// porque msg.ID viria vazio. 403 e não 5xx/429 de propósito: discordgo
// retenta 5xx e dorme no 429; 403 volta na hora como *RESTError.
type panelCreateTransport struct {
	recordingTransport
	alvoChannelID string
	falhar        bool
}

func (t *panelCreateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/channels/"+t.alvoChannelID+"/messages") {
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		t.mu.Lock()
		t.bodies = append(t.bodies, body)
		t.mu.Unlock()

		if t.falhar {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Body:       io.NopCloser(strings.NewReader(`{"code":50013,"message":"Missing Permissions"}`)),
				Header:     make(http.Header),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"id":"777"}`)),
			Header:     make(http.Header),
		}, nil
	}
	return t.recordingTransport.RoundTrip(req)
}

func newDashboardMoveWiringBot(t *testing.T, falhar bool) (*Bot, *panelCreateTransport, *discordgo.InteractionCreate) {
	t.Helper()
	rt := &panelCreateTransport{alvoChannelID: "555", falhar: falhar}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	b := &Bot{
		cfg:     &config.Config{RefreshInterval: time.Minute},
		hosts:   []*dockerx.Client{fakeDockerHost(t, "")},
		session: session,
		limiter: newRateLimiter(8, 0.5),
	}
	b.dashboard = newDashboard(b)
	t.Cleanup(func() {
		if !esperaAuditoria(&b.dashboard.renderWG, 5*time.Second) {
			t.Error("render em voo nao terminou em 5s -- goroutine de render travada")
		}
	})

	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	b.store = st

	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:      discordgo.InteractionApplicationCommand,
		ID:        "1", AppID: "2", Token: "tok",
		ChannelID: "555",
	}}
	return b, rt, i
}

// efemeroConteudoDashboard decodifica os corpos gravados e devolve o "content"
// do PATCH de @original -- a resposta efêmera que o operador vê, distinta do
// POST de criação do painel.
func efemeroConteudoDashboard(t *testing.T, rt *panelCreateTransport) string {
	t.Helper()
	for _, body := range rt.bodiesCopy() {
		var payload struct {
			Content *string `json:"content"`
		}
		if json.Unmarshal(body, &payload) != nil {
			continue
		}
		if payload.Content != nil {
			return *payload.Content
		}
	}
	t.Fatalf("nenhum corpo com campo content (resposta efêmera) nos corpos gravados: %q", rt.all())
	return ""
}

// TestCmdDashboardAvisaQuandoPublicacaoFalha prova que, quando o Discord
// recusa a criação da mensagem-painel (403 Missing Permissions), a resposta
// efêmera NÃO afirma sucesso.
func TestCmdDashboardAvisaQuandoPublicacaoFalha(t *testing.T) {
	b, rt, i := newDashboardMoveWiringBot(t, true)
	b.cmdDashboard(i)

	content := efemeroConteudoDashboard(t, rt)
	if strings.Contains(content, "Painel fixado") {
		t.Fatalf("resposta afirmou sucesso apesar do 403 na criação do painel: %q", content)
	}
	if !strings.Contains(content, "permiss") {
		t.Fatalf("resposta não avisa sobre permissão: %q", content)
	}
}

// TestCmdDashboardConfirmaSucesso é a contraprova: com o POST de criação
// aceito (200 + id), a resposta efêmera CONTINUA afirmando sucesso.
func TestCmdDashboardConfirmaSucesso(t *testing.T) {
	b, rt, i := newDashboardMoveWiringBot(t, false)
	b.cmdDashboard(i)

	content := efemeroConteudoDashboard(t, rt)
	if !strings.Contains(content, "✅ Painel fixado neste canal") {
		t.Fatalf("resposta não confirmou sucesso com o POST aceito: %q", content)
	}
}
