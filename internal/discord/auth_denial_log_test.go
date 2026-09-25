package discord

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// A negação do portão isOwner não deixava rastro nenhum: Command/Component
// respondiam a negação efêmera e Autocomplete/ModalSubmit negavam em
// silêncio absoluto, mas nos 4 casos nada ia para o log. Reusa authBot e
// interacaoDe de auth_wiring_test.go.

func TestOnInteracaoNegadaVaiParaOLog(t *testing.T) {
	tipos := []discordgo.InteractionType{
		discordgo.InteractionApplicationCommand,
		discordgo.InteractionApplicationCommandAutocomplete,
		discordgo.InteractionMessageComponent,
		discordgo.InteractionModalSubmit,
	}
	for _, tipo := range tipos {
		b, _ := authBot(t)
		i := interacaoDe("intruso-666", tipo)
		i.ChannelID = "canal-42"

		var buf bytes.Buffer
		log.SetOutput(&buf)
		b.onInteraction(b.session, i)
		log.SetOutput(os.Stderr)

		saida := buf.String()
		if !strings.Contains(saida, "NEGADA") {
			t.Fatalf("tipo %d: log não contém NEGADA (saída: %q)", tipo, saida)
		}
		if !strings.Contains(saida, "intruso-666") {
			t.Fatalf("tipo %d: log não contém o id do intruso (saída: %q)", tipo, saida)
		}
		if !strings.Contains(saida, "canal-42") {
			t.Fatalf("tipo %d: log não contém o canal (saída: %q)", tipo, saida)
		}
	}
}

func TestOnInteracaoDoDonoNaoVaiParaOLog(t *testing.T) {
	b, _ := authBot(t)
	i := interacaoDe("dono-123", discordgo.InteractionApplicationCommand)
	i.ChannelID = "canal-42"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	b.onInteraction(b.session, i)
	log.SetOutput(os.Stderr)

	if strings.Contains(buf.String(), "NEGADA") {
		t.Fatalf("dono não deveria gerar log de negação (saída: %q)", buf.String())
	}
}

// TestLogNegacaoNaoPermiteForjarLinha prova o %q do logDenied: o username
// é texto controlado por terceiro, e sem aspas um "\n" dentro dele forja uma
// linha de log inteira (ex.: uma falsa "interação do dono"). Escrito na
// drenagem de 25/09/2026 — a mutação %q→%s sobrevivia à suíte da branch.
func TestLogNegacaoNaoPermiteForjarLinha(t *testing.T) {
	b, _ := authBot(t)
	i := interacaoDe("intruso-666", discordgo.InteractionMessageComponent)
	i.Member.User.Username = "intruso\nLINHA-FORJADA"
	i.ChannelID = "canal-42"

	var buf bytes.Buffer
	log.SetOutput(&buf)
	b.onInteraction(b.session, i)
	log.SetOutput(os.Stderr)

	saida := buf.String()
	if strings.Contains(saida, "\nLINHA-FORJADA") {
		t.Fatalf("username com quebra de linha forjou linha de log: %q", saida)
	}
	// Contraprova: o username chegou ao log, escapado (senão a asserção de
	// cima passaria com o campo simplesmente ausente).
	if !strings.Contains(saida, `intruso\nLINHA-FORJADA`) {
		t.Fatalf("username escapado não apareceu no log: %q", saida)
	}
}
