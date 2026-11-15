package discord

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do log de falha de host.State em handleSelect
// (components.go, ramo `err != nil`). O erro vem do commandconn do docker/cli
// quando o host é REMOTO: carrega o argv do ssh e o `stderr=` do sshd/sudo (até
// 4096 bytes, com quebras de linha) NO FIM. Com `%v` cru, um único erro vira N
// entradas no json-file (e no Loki), e uma linha do stderr pode imitar o
// prefixo de uma linha legítima do bot -- a mesma classe já fechada em
// logDenied (auth_denial_log_test.go, TestLogNegacaoNaoPermiteForjarLinha) e
// nos dois sítios de embed.go (embed_log_forja_test.go).
//
// O oráculo tem três partes, e cada uma mata uma mutação diferente:
//   - 1 linha  -> exige o %q (truncar sozinho não escapa o \n do tail);
//   - teto     -> exige o truncateMiddle (o %q sozinho não limita o tamanho);
//   - causa    -> exige que o truncate seja o MIDDLE (o prefixo descarta
//     exatamente o `stderr=` que explica a falha).
//
// O Content da resposta NÃO muda: isso é coberto por
// TestHandleSelectErroLongoRespeitaTetoDe2000 (action_erro_teto_wiring_test.go).
func TestHandleSelectLogDeErroEmUmaLinhaComCausa(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	// O `\n` da raw string é escape JSON dentro de {"message":"..."} (ver
	// inspectErroLongoHost), então o SDK devolve uma quebra de linha DE VERDADE
	// dentro de err.Error(). A linha forjada imita o prefixo do json-file.
	msg := strings.Repeat("x", 1500) +
		`\n2026/11/13 03:00:00 [dockerstats] LINHA-FORJADA\n` +
		"CAUSA-NO-FIM"

	b := &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{inspectErroLongoHost(t, msg)},
		session: session,
	}

	b.handleSelect(selectInteraction("main:web"))

	saida := buf.String()
	// Controle positivo de CAMINHO: sem isto, um stub que respondesse 404
	// (ErrNotFound) passaria as asserções abaixo pelo motivo errado -- saída
	// vazia tem 0 quebras de linha e 0 bytes.
	if !strings.Contains(saida, "select state") {
		t.Fatalf("não passou pelo ramo de falha de host.State: %.300q", saida)
	}
	if n := strings.Count(saida, "\n"); n != 1 {
		t.Fatalf("erro do inspect virou %d linhas no log (esperado 1, o terminador do log.Printf): %.400q", n, saida)
	}
	if !strings.Contains(saida, "CAUSA-NO-FIM") {
		t.Fatalf("log perdeu a causa no fim do erro: %.400q", saida)
	}
	if len(saida) > 600 {
		t.Fatalf("linha de log sem teto: %d bytes; %.200q", len(saida), saida)
	}
}
