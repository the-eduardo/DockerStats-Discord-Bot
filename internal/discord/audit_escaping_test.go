package discord

import (
	"encoding/json"
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

// e.target vem de opcao de slash command com Autocomplete: true, que NAO
// restringe o valor enviado (commands.go:18-24) — e' texto livre igual ao
// Detalhe do teste acima. Sem teto, um nome longo estoura o limite de 1024 do
// campo do embed e o Discord rejeita o embed inteiro: o registro da acao se
// perde por completo, em silencio. Sem escape, um backtick no nome fecha a
// cerca cedo. E o Resultado interpola o mesmo texto livre (runAction,
// components.go) sem cerca nenhuma.
func TestAuditNomeLongoNaoEstouraOCampoENaoRenderizaMarkdown(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session, cfg: &config.Config{AuditChannelID: "999"}}

	nomeLongo := strings.Repeat("a", 3000) + "`[x](http://p.example)"
	b.audit(auditEntry{
		actor:  "eduardo",
		action: "stop",
		host:   "main",
		target: nomeLongo,
		result: "✅ `b`[y](http://p.example)` parado em Main.",
	})

	deadline := time.After(2 * time.Second)
	var corpo string
	for {
		corpo = string(rt.all())
		if strings.Contains(corpo, "Container") {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("o embed de auditoria nao foi enviado; corpo: %q", corpo)
		case <-time.After(10 * time.Millisecond):
		}
	}

	var payload struct {
		Embeds []struct {
			Fields []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"fields"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(rt.all(), &payload); err != nil {
		t.Fatalf("payload nao decodificou como JSON do embed: %v; corpo: %s", err, corpo)
	}
	if len(payload.Embeds) == 0 {
		t.Fatalf("nenhum embed no payload: %s", corpo)
	}

	var containerVal, resultadoVal string
	for _, f := range payload.Embeds[0].Fields {
		switch f.Name {
		case "Container":
			containerVal = f.Value
		case "Resultado":
			resultadoVal = f.Value
		}
	}

	if n := len([]rune(containerVal)); n > 1024 {
		t.Fatalf("campo Container com %d runes, estoura o limite de 1024 do Discord", n)
	}
	if strings.Contains(containerVal, "`[x](http://p.example)") {
		t.Errorf("backtick+link do usuario sobreviveram em Container: %q", containerVal)
	}
	if strings.Contains(resultadoVal, "`[y](http://p.example)`") {
		t.Errorf("backtick+link do usuario sobreviveram em Resultado: %q", resultadoVal)
	}

	// Controle positivo: escapar nao pode virar censura.
	if !strings.HasPrefix(containerVal, "`aaaa") {
		t.Errorf("prefixo legivel do nome sumiu de Container: %q", containerVal)
	}
	if !strings.Contains(resultadoVal, "parado em Main.") {
		t.Errorf("texto legivel sumiu de Resultado: %q", resultadoVal)
	}
}
