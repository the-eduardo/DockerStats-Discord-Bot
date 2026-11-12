package discord

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do teto de 2000 chars nos dois ÚLTIMOS sítios da
// classe já corrigida no /exec (ops.go:298) e no /logs (ops.go:128/:200):
//
//   - runAction (components.go, case err != nil): a string volta como Content
//     do PATCH de publishActionResult E como Resultado da auditoria em
//     runActionAudited. Com err.Error() cru, um erro longo (o do ssh remoto
//     carrega o argv e o stderr do sshd/sudo, até 4096 bytes, NO FIM) estoura o
//     Content, o Discord responde 400 e o operador fica sem resposta nenhuma --
//     a falha do PATCH aqui só vai para o log, de propósito. Na auditoria a
//     causa também se perdia, porque audit() corta o Resultado em 1000 runes
//     pelo PREFIXO.
//   - handleSelect (components.go, falha de host.State): mesmo erro cru indo
//     para o Content.
//
// O oráculo dos dois é o tamanho (<= maxMessage) MAIS a presença do marcador
// que só existe no FIM do erro: truncate() comum passaria o teste de tamanho e
// falharia o de causa, que é exatamente o modo de falha que motivou o
// truncateMiddle.

// TestHandleActionErroLongoRespeitaTetoDe2000 cobre o caminho de ciclo de vida
// (botão start) com o inspect do ensureExists falhando com uma mensagem grande.
func TestHandleActionErroLongoRespeitaTetoDe2000(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "canal-auditoria"
	// Substitui o host do construtor: erroInspectLongoHost reaponta o
	// DOCKER_HOST para um stub cujo /containers/.../json responde 500 com 3000
	// chars terminando em CAUSA-NO-FIM. Não é ErrNotFound (404), então runAction
	// cai no ramo `err != nil`, não no de container inexistente.
	b.hosts = []*dockerx.Client{erroInspectLongoHost(t)}

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	b.auditWG.Wait()

	// publishActionResult é SÍNCRONO e roda antes de refreshAfterAction, então o
	// primeiro corpo com campo "content" é o PATCH do resultado da ação (o
	// callback do defer traz "type"/"data"; o painel, "embeds").
	content := efemeroConteudo(t, &rt.recordingTransport)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("resposta do erro de start estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "CAUSA-NO-FIM") {
		t.Fatalf("resposta não preservou a causa no fim do erro: %.300q", content)
	}

	resultado := strings.TrimPrefix(auditResultado(t, &rt.recordingTransport), "```\n")
	if !strings.HasPrefix(resultado, "⚠️ Erro ao start") {
		t.Fatalf("auditoria não começou com o prefixo de erro esperado: %.200q", resultado)
	}
	if !strings.Contains(resultado, "CAUSA-NO-FIM") {
		t.Fatalf("auditoria não preservou a causa no fim do erro (audit() corta em 1000 pelo PREFIXO): %.300q", resultado)
	}
}

// TestHandleSelectErroLongoRespeitaTetoDe2000 cobre o outro sítio: a falha de
// host.State ao montar os botões de ação do container escolhido no select.
func TestHandleSelectErroLongoRespeitaTetoDe2000(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{erroInspectLongoHost(t)},
		session: session,
	}

	b.handleSelect(selectInteraction("main:web"))

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("resposta do erro do select estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, "CAUSA-NO-FIM") {
		t.Fatalf("resposta não preservou a causa no fim do erro: %.300q", content)
	}
	// Controle positivo de CAMINHO: prova que o teste exercitou o ramo de falha
	// de host.State, não o de ErrNotFound nem o de host desconhecido -- sem
	// isto, um stub que devolvesse 404 passaria as duas asserções acima pelo
	// motivo errado (mensagem curta, sem teto nenhum aplicado).
	if !strings.Contains(content, "Falha ao consultar") {
		t.Fatalf("o teste não passou pelo ramo de erro de host.State: %.300q", content)
	}
}

// TestRunActionTruncaNomeLongoDoSlashCommand prova a metade do teto que o erro
// truncado sozinho não fecha: cmdContainerAction (commands.go) recebe o nome do
// container de uma opção com Autocomplete, que NÃO restringe o valor -- é texto
// livre sem MaxLength. Com um nome de 3000 chars, truncar só o err.Error()
// ainda estouraria os 2000 do Content.
func TestRunActionTruncaNomeLongoDoSlashCommand(t *testing.T) {
	b := &Bot{
		cfg:   &config.Config{},
		hosts: []*dockerx.Client{erroInspectLongoHost(t)},
	}

	res := b.runAction(t.Context(), "main", "start", strings.Repeat("n", 3000))

	if n := len([]rune(res)); n > maxMessage {
		t.Fatalf("erro de start com nome longo estourou o teto do Discord: %d runes (max %d)", n, maxMessage)
	}
	if !strings.Contains(res, "CAUSA-NO-FIM") {
		t.Fatalf("a causa no fim do erro foi perdida: %.300q", res)
	}
}
