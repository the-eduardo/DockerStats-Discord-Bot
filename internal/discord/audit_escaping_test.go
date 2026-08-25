package discord

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// O canal de auditoria e o UNICO registro duravel da superficie perigosa do bot
// (/exec, stop, restart). O campo Detalhe carrega o comando cru do /exec — texto
// livre de quem chamou. Sem cerca, esse texto e' renderizado como markdown no
// embed: link mascarado, ||spoiler|| escondendo o comando real, ou uma cerca de
// backtick quebrando a formatacao. Quem aciona ja tem permissao de exec, entao
// nao ha escalacao de privilegio — o que se perde e a CONFIABILIDADE do registro,
// que e justamente o que ele existe para dar. Achado do painel AppSec na
// drenagem de 25/08/2026.
func TestAuditDetalheNaoRenderizaMarkdownDoUsuario(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session, cfg: &config.Config{AuditChannelID: "999"}}

	const veneno = "sh -c 'rm -rf /' ||spoiler|| [abrir log](http://phishing.example) `cerca`"
	b.audit(auditEntry{actor: "eduardo", action: "exec", host: "main", target: "web", detail: veneno, result: "✅ executado"})

	// audit() e assincrono de proposito (nao pode atrasar a acao principal).
	deadline := time.After(2 * time.Second)
	var corpo string
	for {
		corpo = string(rt.all())
		if strings.Contains(corpo, "Detalhe") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("o embed de auditoria nao foi enviado; corpo: %q", corpo)
		case <-time.After(10 * time.Millisecond):
		}
	}

	// O conteudo tem que estar DENTRO de uma cerca de codigo — e' isso que torna
	// literal o markdown do usuario.
	if !strings.Contains(corpo, `\n`+"```") && !strings.Contains(corpo, "```") {
		t.Errorf("o Detalhe nao esta dentro de cerca de codigo; corpo: %s", corpo)
	}
	// E nenhum backtick do usuario pode ter sobrevivido para fechar a cerca:
	// o texto injetado tinha `cerca`, que viraria 'cerca'.
	if strings.Contains(corpo, "`cerca`") {
		t.Errorf("backtick do usuario sobreviveu e pode fechar a cerca; corpo: %s", corpo)
	}
	// Controle positivo: o comando em si TEM que continuar legivel no registro —
	// escapar nao pode virar censura, senao a auditoria perde a informacao.
	if !strings.Contains(corpo, "rm -rf /") {
		t.Errorf("o comando auditado sumiu do registro; corpo: %s", corpo)
	}
}
