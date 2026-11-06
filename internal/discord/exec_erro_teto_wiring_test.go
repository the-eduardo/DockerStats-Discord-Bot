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

// Este arquivo testa a FIAÇÃO do teto de 2000 chars no ramo de ERRO de
// handleModal (ops.go, case err != nil). O ramo de sucesso já respeita o
// teto via execBody; o de erro mandava err.Error() inteiro tanto para o
// Content da resposta efêmera (estourando o PATCH com 400 quando o erro é
// longo — ex.: falha de ssh remoto, argv + "stderr=" no fim) quanto para o
// Resultado da auditoria (que o audit() corta em 1000 runes pelo PREFIXO,
// perdendo exatamente a causa no fim). Mesma classe já corrigida em
// embed.go:34 (01/11/2026).

// erroInspectLongoHost sobe um stub da API do Docker cujo /containers/.../json
// (usado por ensureExists, chamado no início de Exec) responde 500 com uma
// mensagem de erro BEM longa terminando num marcador distintivo — reproduz um
// erro remoto de ssh com a causa real no fim do texto.
func erroInspectLongoHost(t *testing.T) *dockerx.Client {
	t.Helper()
	msgLonga := strings.Repeat("x", 3000) + " CAUSA-NO-FIM"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"` + msgLonga + `"}`))
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

// TestHandleModalErroDeExecRespeitaOTetoDe2000 prova que um erro LONGO de
// host.Exec (ensureExists falhando com 500 de mensagem grande) não estoura o
// Content da resposta efêmera e preserva a causa (que fica no FIM do erro de
// ssh real) tanto na resposta quanto na auditoria.
func TestHandleModalErroDeExecRespeitaOTetoDe2000(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{erroInspectLongoHost(t)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}

	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("resposta efêmera do erro de /exec estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "CAUSA-NO-FIM") {
		t.Fatalf("resposta efêmera não preservou a causa no fim do erro: %.300q", content)
	}

	resultado := strings.TrimPrefix(auditResultado(t, rt), "```\n")
	if !strings.HasPrefix(resultado, "⚠️ erro:") {
		t.Fatalf("auditoria não começou com o prefixo de erro esperado: %q", resultado)
	}
	if !strings.Contains(resultado, "CAUSA-NO-FIM") {
		t.Fatalf("auditoria não preservou a causa no fim do erro: %q", resultado)
	}
}
