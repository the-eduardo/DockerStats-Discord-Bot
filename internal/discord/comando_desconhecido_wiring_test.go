package discord

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// TestOnInteractionComandoDesconhecidoRespondeERegistra prova a FIAÇÃO do
// default de handleCommand (achado do enxame, 29/09/2026): antes desta
// mudança, um nome de comando fora dos 9 de commandDefs (registro
// remanescente de escopo antigo, ou refactor futuro) fazia handleCommand
// retornar em silêncio — o dono via "This interaction failed" sem nenhum
// rastro para diagnosticar. Exercita b.onInteraction (o caminho que chama),
// não handleCommand isolado. Reusa authBot/interacaoDe de
// auth_wiring_test.go — interacaoDe já crava Name="comando-inexistente" para
// o tipo InteractionApplicationCommand.
func TestOnInteractionComandoDesconhecidoRespondeERegistra(t *testing.T) {
	b, rt := authBot(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b.onInteraction(b.session, interacaoDe("dono-123", discordgo.InteractionApplicationCommand))

	corpo := string(rt.all())
	if !strings.Contains(corpo, "Comando desconhecido") {
		t.Fatalf("o dono ficou sem resposta ao comando desconhecido; corpo: %.120q", corpo)
	}
	// Contraprova anti-eco: o nome vindo do cliente não pode voltar ao
	// Discord — mesma regra do default de handleAction (não ecoar o verbo).
	if strings.Contains(corpo, "comando-inexistente") {
		t.Fatalf("o default ecoou data.Name no Content; corpo: %.120q", corpo)
	}
	// E precisa deixar rastro no log, senão o clique some sem diagnóstico.
	if !strings.Contains(buf.String(), `"comando-inexistente"`) {
		t.Fatalf("comando desconhecido não foi registrado no log; log: %q", buf.String())
	}
}
