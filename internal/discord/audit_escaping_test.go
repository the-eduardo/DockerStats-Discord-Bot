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

// auditFields dispara b.audit(e), espera o embed assincrono chegar no
// transporte fake e devolve o valor de cada campo do embed por nome
// ("Container", "Resultado", ...). Usado pelos testes de isolamento abaixo —
// cada um MUTA um unico campo do payload para exercitar exatamente UMA
// defesa (teto OU escape OU cerca), nunca duas ao mesmo tempo, para que uma
// mutacao no codigo derrube exatamente um teste (achado do QA na drenagem de
// 29/08/2026: payload com propriedade acidental deixando uma defesa mais
// fraca neutralizar o payload antes da defesa-alvo agir).
func auditFields(t *testing.T, e auditEntry, waitFieldName string) map[string]string {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session, cfg: &config.Config{AuditChannelID: "999"}}

	b.audit(e)

	deadline := time.After(2 * time.Second)
	var corpo []byte
	for {
		corpo = rt.all()
		if strings.Contains(string(corpo), waitFieldName) {
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
	if err := json.Unmarshal(corpo, &payload); err != nil {
		t.Fatalf("payload nao decodificou como JSON do embed: %v; corpo: %s", err, corpo)
	}
	if len(payload.Embeds) == 0 {
		t.Fatalf("nenhum embed no payload: %s", corpo)
	}
	out := map[string]string{}
	for _, f := range payload.Embeds[0].Fields {
		out[f.Name] = f.Value
	}
	return out
}

// (1) TETO do Container, isolado do escape: payload SEM nenhum backtick, so
// texto longo. Com o backtick ausente, a mutacao que remove o ReplaceAll do
// Container e' um no-op sobre este payload — quem quebra este teste e'
// UNICAMENTE a mutacao que remove o truncate.
func TestAuditContainerTetoNaoEstoura(t *testing.T) {
	nomeLongo := strings.Repeat("z", 2000)
	fields := auditFields(t, auditEntry{
		actor: "eduardo", action: "stop", host: "main", target: nomeLongo,
	}, "Container")

	containerVal := fields["Container"]
	if n := len([]rune(containerVal)); n > 1024 {
		t.Fatalf("campo Container com %d runes, estoura o limite de 1024 do Discord; teto ausente", n)
	}
	// Controle positivo: o teto nao pode virar censura total do prefixo legivel.
	if !strings.HasPrefix(containerVal, "`zzzz") {
		t.Errorf("prefixo legivel do nome sumiu de Container: %q", containerVal)
	}
}

// (2) ESCAPE do Container, isolado do teto: backtick + link DENTRO das
// primeiras 250 runes (bem dentro da janela do teto), payload curto o
// suficiente para o truncate nunca entrar em jogo. So a mutacao que remove o
// ReplaceAll do backtick no Container derruba este teste — a mutacao do teto
// e' irrelevante aqui porque o payload nunca chega perto de 250 runes.
func TestAuditContainerEscapeRemoveBacktickELink(t *testing.T) {
	const alvoMalicioso = "web`[x](http://p.example)"
	if n := len([]rune(alvoMalicioso)); n >= 250 {
		t.Fatalf("payload de teste mal desenhado: %d runes, precisa ficar bem abaixo de 250", n)
	}
	fields := auditFields(t, auditEntry{
		actor: "eduardo", action: "stop", host: "main", target: alvoMalicioso,
	}, "Container")

	containerVal := fields["Container"]
	if strings.Contains(containerVal, "`[x](http://p.example)") {
		t.Errorf("backtick+link do usuario sobreviveram em Container: %q", containerVal)
	}
	// Controle positivo: escapar nao pode virar censura do nome legivel.
	if !strings.Contains(containerVal, "web") {
		t.Errorf("texto legivel sumiu de Container: %q", containerVal)
	}
}

// (3) ESCAPE do Resultado, isolado da cerca: payload com backtick mas SEM
// nenhum link, curto (bem abaixo de 1000 runes). A cerca (```) permanece
// intacta com ou sem o escape — quem muda de estado com a mutacao e' so a
// presenca do backtick cru DENTRO da cerca.
func TestAuditResultadoEscapeRemoveBacktick(t *testing.T) {
	fields := auditFields(t, auditEntry{
		actor: "eduardo", action: "stop", host: "main", target: "web",
		result: "✅ `web` parado em Main.",
	}, "Resultado")

	resultadoVal := fields["Resultado"]
	if strings.Contains(resultadoVal, "`web`") {
		t.Errorf("backtick do usuario sobreviveu em Resultado: %q", resultadoVal)
	}
	// Controle positivo: escapar nao pode virar censura do texto legivel.
	if !strings.Contains(resultadoVal, "parado em Main.") {
		t.Errorf("texto legivel sumiu de Resultado: %q", resultadoVal)
	}
}

// (4) CERCA do Resultado, isolado do escape: link SEM nenhum backtick,
// abaixo de 1000 runes. Sem backtick no payload, a mutacao que remove o
// ReplaceAll do backtick no Resultado e' um no-op aqui — so a mutacao que
// remove a cerca (```) muda o resultado deste teste.
func TestAuditResultadoCercaEnvolveLinkSemBacktick(t *testing.T) {
	const resultadoMalicioso = "✅ [abrir log](http://p.example) executado."
	if strings.Contains(resultadoMalicioso, "`") {
		t.Fatalf("payload de teste mal desenhado: contem backtick")
	}
	if n := len([]rune(resultadoMalicioso)); n >= 1000 {
		t.Fatalf("payload de teste mal desenhado: %d runes, precisa ficar abaixo de 1000", n)
	}
	fields := auditFields(t, auditEntry{
		actor: "eduardo", action: "stop", host: "main", target: "web",
		result: resultadoMalicioso,
	}, "Resultado")

	resultadoVal := fields["Resultado"]
	if !strings.HasPrefix(resultadoVal, "```\n") || !strings.HasSuffix(resultadoVal, "\n```") {
		t.Errorf("Resultado nao esta envolto em cerca de codigo; a ausencia da cerca deixa o link do usuario renderizar como markdown ativo: %q", resultadoVal)
	}
	// Controle positivo: a cerca nao pode virar censura do texto legivel.
	if !strings.Contains(resultadoVal, "executado.") {
		t.Errorf("texto legivel sumiu de Resultado: %q", resultadoVal)
	}
}
