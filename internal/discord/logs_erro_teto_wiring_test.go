package discord

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do teto de 2000 chars nos dois ramos de ERRO do
// /logs (cmdLogs e showLogsEphemeral, chamado pelo botão "Logs" do painel).
// Os dois mandavam err.Error() cru tanto para o Content da resposta quanto
// para o Resultado da auditoria — um erro de ssh remoto carrega o argv +
// "stderr=" no FIM, estourando o PATCH (400, operador sem resposta) e
// perdendo a causa quando o audit() corta pelo PREFIXO. Mesma classe já
// corrigida no /exec (ver exec_erro_teto_wiring_test.go) e em embed.go:34.

// inspectErroLongoHost sobe um stub da API do Docker cujo /containers/.../json
// (chamado por Logs antes de ler o stream) responde 500 com a mensagem dada —
// reproduz um erro remoto de ssh com a causa real no fim de um texto longo.
func inspectErroLongoHost(t *testing.T, msg string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"` + msg + `"}`))
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

// TestLogsErroRespeitaOTetoDe2000 prova, nos dois chamadores de Logs
// (cmdLogs via /logs e showLogsEphemeral via botão), que um erro LONGO não
// estoura o Content da resposta e preserva a causa no fim tanto na resposta
// quanto na auditoria.
func TestLogsErroRespeitaOTetoDe2000(t *testing.T) {
	msg := strings.Repeat("argv-ssh-ruido ", 250) + "stderr=MARCA_CAUSA_FIM"

	novoBot := func(t *testing.T) (*Bot, *recordingTransport) {
		t.Helper()
		rt := &recordingTransport{}
		session, err := discordgo.New("Bot token-de-teste")
		if err != nil {
			t.Fatalf("discordgo.New: %v", err)
		}
		session.Client = &http.Client{Transport: rt}
		return &Bot{
			cfg:     &config.Config{AuditChannelID: "999"},
			hosts:   []*dockerx.Client{inspectErroLongoHost(t, msg)},
			session: session,
			limiter: newRateLimiter(100, 1),
		}, rt
	}

	assertConteudoETeto := func(t *testing.T, rt *recordingTransport) {
		t.Helper()
		var content string
		for _, body := range rt.bodiesCopy() {
			if strings.Contains(string(body), "Erro ao ler logs") {
				content = string(body)
				break
			}
		}
		if content == "" {
			t.Fatalf("nenhum corpo com 'Erro ao ler logs' nos envios gravados: %q", rt.all())
		}
		if n := len([]rune(content)); n > maxMessage {
			t.Fatalf("corpo da resposta de erro do /logs estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
		}
		if !strings.Contains(content, "MARCA_CAUSA_FIM") {
			t.Fatalf("resposta de erro não preservou a causa no fim do erro: %.300q", content)
		}
		resultado := strings.TrimPrefix(auditResultado(t, rt), "```\n")
		if !strings.HasPrefix(resultado, "⚠️") {
			t.Fatalf("auditoria não começou com o prefixo de erro esperado: %q", resultado)
		}
		if !strings.Contains(resultado, "MARCA_CAUSA_FIM") {
			t.Fatalf("auditoria não preservou a causa no fim do erro: %q", resultado)
		}
	}

	t.Run("cmdLogs", func(t *testing.T) {
		b, rt := novoBot(t)
		b.cmdLogs(logsInteraction())
		b.auditWG.Wait()
		assertConteudoETeto(t, rt)
	})

	t.Run("botao logs (handleAction)", func(t *testing.T) {
		b, rt := novoBot(t)
		b.handleAction(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			ID:   "1", AppID: "2", Token: tokenDaInteracao,
			Data: discordgo.MessageComponentInteractionData{CustomID: "act:logs:main:web"},
		}}, "act:logs:main:web")
		b.auditWG.Wait()
		assertConteudoETeto(t, rt)
	})
}
