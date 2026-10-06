package discord

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// TestRenderAchataLogDeErroTransitorio prova o motivo de existir errLine: um
// *discordgo.RESTError imprime o CORPO da resposta HTTP verbatim, e um 5xx de
// borda (Cloudflare, etc.) costuma devolver página HTML multi-linha. Sem
// achatamento, cada "\n" do corpo vira uma ENTRADA SEPARADA no log json-file,
// órfã do prefixo "dashboard: erro transitorio" que identifica a causa.
//
// Exercita d.render() -- o mesmo método que loop() chama em produção -- e não
// um helper isolado, porque a fiação importa: a formatação só protege de
// verdade se estiver no caminho real que escreve no log.
func TestRenderAchataLogDeErroTransitorio(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)

	// 1º render: cria o painel (200 OK), fixando messageID. O bool de retorno
	// reflete "vivo" (hosts=nil aqui), não "publicado" -- ver comentário de
	// moveTo() em dashboard.go -- por isso a prova de criação é o messageID.
	d.render()
	if d.messageID != "msg-1" {
		t.Fatalf("messageID esperado 'msg-1' após criação, veio %q", d.messageID)
	}

	// 2º render: simula um 503 de borda com corpo HTML multi-KB -- o modo
	// normal do Cloudflare num 5xx, e o pior caso para um log que não achata.
	transport.editStatus = 503
	transport.editBody = "<html>\n<head><title>503</title></head>\n<body>" +
		strings.Repeat("x", 4000) + "</body>\n</html>"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr) // restaura o padrão -- nil quebraria log.Printf nos testes seguintes

	d.render()

	out := buf.String()

	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("esperava exatamente 1 linha de log (1 '\\n'), veio %d -- o corpo do erro vazou multi-linha:\n%s", got, out)
	}
	if strings.Contains(out, "</html>") {
		t.Fatalf("o corpo HTML do erro não foi truncado, vazou inteiro no log: %s", out)
	}
	if n := len([]rune(out)); n >= 450 {
		t.Fatalf("log de erro não foi truncado: %d runes (esperava < 450)", n)
	}
	if !strings.Contains(out, "HTTP 503") {
		t.Fatalf("a causa do erro (HTTP 503) não sobreviveu ao truncamento: %s", out)
	}

	// Guarda de regressão: 503 não é ErrCodeUnknownMessage, então o ramo
	// transitório NUNCA pode zerar messageID -- fazer isso criaria um painel
	// duplicado no próximo ciclo (ver comentário de render() em dashboard.go).
	if d.messageID != "msg-1" {
		t.Fatalf("erro transitório (503) não deveria zerar messageID, mas zerou: %q", d.messageID)
	}
}
