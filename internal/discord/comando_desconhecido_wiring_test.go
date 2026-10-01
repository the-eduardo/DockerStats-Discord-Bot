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

// TestOnInteractionComandoDesconhecidoLogTemTeto prova o teto de 32 runes do
// nome no log (padrão do logDenied): o nome vem do cliente e não pode inflar
// a linha de log sem limite. Drenagem 30/09/2026: remover o truncate deixava
// a suíte verde porque o nome do helper interacaoDe é curto.
func TestOnInteractionComandoDesconhecidoLogTemTeto(t *testing.T) {
	b, _ := authBot(t)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	longo := strings.Repeat("z", 200)
	i := interacaoDe("dono-123", discordgo.InteractionApplicationCommand)
	i.Data = discordgo.ApplicationCommandInteractionData{Name: longo}
	b.onInteraction(b.session, i)

	logado := buf.String()
	if !strings.Contains(logado, "comando desconhecido") {
		t.Fatalf("comando desconhecido não foi registrado no log: %q", logado)
	}
	if strings.Contains(logado, strings.Repeat("z", 33)) {
		t.Fatalf("log não truncou o nome do comando em 32 runes: %q", logado)
	}
}
