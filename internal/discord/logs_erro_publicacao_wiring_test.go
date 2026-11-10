package discord

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Complemento de logs_erro_teto_wiring_test.go, escrito na drenagem de
// 10/11/2026: a branch do teto no erro do /logs trouxe DUAS mudanças de
// comportamento além do truncateMiddle, e nenhuma tinha teste — remover
// qualquer uma deixava a suíte verde (verificado por mutação):
//   1. se o editResponse da mensagem de erro falha, a auditoria registra
//      "(resposta não publicada: ...)" com o erro redigido por errSafe;
//   2. o nome do container no cabeçalho da resposta sai cortado em 100 runes.
// Os dois chamadores (cmdLogs e o botão "Logs" via handleAction) são cobertos.

func novoBotLogsErro(t *testing.T, msg string) (*Bot, *recordingTransport) {
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

func logsInteractionNome(nome string) *discordgo.InteractionCreate {
	i := logsInteraction()
	data := i.Data.(discordgo.ApplicationCommandInteractionData)
	data.Options = []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: nome},
	}
	i.Data = data
	return i
}

func botaoLogsInteraction(nome string) (*discordgo.InteractionCreate, string) {
	cid := "act:logs:main:" + nome
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		ID:   "1", AppID: "2", Token: tokenDaInteracao,
		Data: discordgo.MessageComponentInteractionData{CustomID: cid},
	}}, cid
}

// chamadores roda o mesmo cenário pelos dois caminhos que leem logs.
var chamadoresLogs = []struct {
	nome string
	run  func(b *Bot, container string)
}{
	{"cmdLogs", func(b *Bot, c string) { b.cmdLogs(logsInteractionNome(c)) }},
	{"botao logs (handleAction)", func(b *Bot, c string) {
		i, cid := botaoLogsInteraction(c)
		b.handleAction(i, cid)
	}},
}

// TestLogsErroNaoPublicadoFicaNaAuditoria: com o PATCH da resposta de erro
// falhando por transporte (o caminho cujo *url.Error carrega o token na URL),
// a auditoria TEM que dizer que a resposta não foi publicada — e sem vazar o
// token. Contraprova no mesmo teste: com o PATCH ok, a marca NÃO aparece
// (garante que a asserção positiva distingue os dois caminhos).
func TestLogsErroNaoPublicadoFicaNaAuditoria(t *testing.T) {
	for _, ch := range chamadoresLogs {
		t.Run(ch.nome+"/patch falha", func(t *testing.T) {
			b, rt := novoBotLogsErro(t, "stderr=MARCA_CAUSA_FIM")
			rt.netErrEdit = true
			ch.run(b, "web")
			b.auditWG.Wait()
			res := auditResultado(t, rt)
			if !strings.Contains(res, "resposta não publicada") {
				t.Fatalf("auditoria muda sobre a resposta de erro não publicada: %q", res)
			}
			if !strings.Contains(res, "MARCA_CAUSA_FIM") {
				t.Fatalf("auditoria perdeu a causa original do erro: %q", res)
			}
			// Prefixo, não o token inteiro: o Resultado corta o erro do PATCH
			// em 80 runes e a URL ("Patch \"https://discord.com/.../2/") já
			// come ~45 — o token vazado sairia truncado e o Contains do token
			// inteiro daria falso-verde.
			if strings.Contains(res, tokenDaInteracao[:16]) {
				t.Fatalf("auditoria vazou (parte d)o token da interação: %q", res)
			}
		})
		t.Run(ch.nome+"/patch ok (contraprova)", func(t *testing.T) {
			b, rt := novoBotLogsErro(t, "stderr=MARCA_CAUSA_FIM")
			ch.run(b, "web")
			b.auditWG.Wait()
			res := auditResultado(t, rt)
			if !strings.Contains(res, "MARCA_CAUSA_FIM") {
				t.Fatalf("contraprova: auditoria sem a causa do erro: %q", res)
			}
			if strings.Contains(res, "resposta não publicada") {
				t.Fatalf("contraprova: marcou 'não publicada' com o PATCH ok: %q", res)
			}
		})
	}
}

// TestLogsErroNomeLongoCortadoNoCabecalho: nome de container com mais de 100
// runes sai cortado em 100 no cabeçalho "Erro ao ler logs de `...`". O nome é
// não-periódico de propósito: com um nome de letra repetida, os primeiros 100
// runes seguidos de crase casariam por acidente no FIM do nome inteiro.
func TestLogsErroNomeLongoCortadoNoCabecalho(t *testing.T) {
	var sb strings.Builder
	for k := 0; k < 20; k++ {
		fmt.Fprintf(&sb, "seg%03d-", k)
	}
	nome := sb.String() // 140 runes
	if len([]rune(nome)) <= 100 {
		t.Fatalf("fixture: nome precisa passar de 100 runes, tem %d", len([]rune(nome)))
	}
	cortado := "Erro ao ler logs de `" + truncate(nome, 100) + "`"
	inteiro := "Erro ao ler logs de `" + nome + "`"

	for _, ch := range chamadoresLogs {
		t.Run(ch.nome, func(t *testing.T) {
			b, rt := novoBotLogsErro(t, "stderr=MARCA_CAUSA_FIM")
			ch.run(b, nome)
			b.auditWG.Wait()
			var content string
			for _, body := range rt.bodiesCopy() {
				if strings.Contains(string(body), "Erro ao ler logs") {
					content = string(body)
					break
				}
			}
			if content == "" {
				t.Fatalf("nenhum corpo com 'Erro ao ler logs': %q", rt.all())
			}
			if !strings.Contains(content, cortado) {
				t.Fatalf("cabeçalho não cortou o nome em 100 runes: %.400q", content)
			}
			if strings.Contains(content, inteiro) {
				t.Fatalf("cabeçalho levou o nome inteiro (%d runes): %.400q", len([]rune(nome)), content)
			}
		})
	}
}
