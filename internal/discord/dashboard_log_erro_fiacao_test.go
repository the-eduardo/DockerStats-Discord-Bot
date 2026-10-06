package discord

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

// Fiacao dos outros dois sitios de log de render() que usam errLine: a criacao
// do painel e o "painel apagado" (404 code 10008). So o ramo transitorio tinha
// teste; trocar errLine(err) por err nos outros dois sobrevivia (mutacao da
// drenagem de 06/10/2026).

func capturaLogRender(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	f()
	return buf.String()
}

func TestRenderAchataLogDeErroDeCriacao(t *testing.T) {
	transport := &fakeDiscordTransport{createStatus: 503, createBody: "<html>\n<body>" + strings.Repeat("x", 4000) + "</body>\n</html>"}
	d := newTestDashboard(t, transport)

	out := capturaLogRender(t, func() { d.render() })

	if !strings.Contains(out, "erro ao criar painel") || !strings.Contains(out, "HTTP 503") {
		t.Fatalf("log de criacao ausente ou sem a causa: %q", out)
	}
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("esperava 1 linha, veio %d: %q", got, out)
	}
	if strings.Contains(out, "</html>") || len([]rune(out)) >= 450 {
		t.Fatalf("corpo nao truncado: %d runes", len([]rune(out)))
	}
}

func TestRenderAchataLogDePainelApagado(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)
	d.render()
	if d.messageID != "msg-1" {
		t.Fatalf("setup: messageID %q", d.messageID)
	}
	transport.editStatus = 404
	transport.editBody = "{\n\"code\": 10008,\n\"message\": \"Unknown Message " + strings.Repeat("y", 4000) + "\"\n}"

	out := capturaLogRender(t, func() { d.render() })

	if !strings.Contains(out, "painel apagado, recriando") {
		t.Fatalf("ramo 'painel apagado' nao exercitado: %q", out)
	}
	if got := strings.Count(out, "\n"); got != 1 {
		t.Fatalf("esperava 1 linha, veio %d: %q", got, out)
	}
	if len([]rune(out)) >= 450 {
		t.Fatalf("log nao truncado: %d runes", len([]rune(out)))
	}
	if d.messageID != "" {
		t.Fatalf("painel apagado deveria zerar messageID, veio %q", d.messageID)
	}
}
