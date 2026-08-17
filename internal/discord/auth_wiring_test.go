package discord

import (
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// isOwner é o ÚNICO portão de autorização de um bot que controla containers em
// 2 hosts — e até 17/08/2026 não tinha um teste sequer (achado do comitê:
// remover a checagem de um dos 4 tipos de interação deixaria a suíte verde).
// Aqui a FIAÇÃO é exercitada: onInteraction real, nos 4 tipos, com intruso e
// com o dono. Reusa o recordingTransport do ops_wiring_test.go.

func authBot(t *testing.T) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{OwnerID: "dono-123"},
		session: session,
	}, rt
}

func interacaoDe(userID string, tipo discordgo.InteractionType) *discordgo.InteractionCreate {
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type:   tipo,
		ID:     "1",
		AppID:  "2",
		Token:  "tok",
		Member: &discordgo.Member{User: &discordgo.User{ID: userID}},
	}}
	switch tipo {
	case discordgo.InteractionApplicationCommand, discordgo.InteractionApplicationCommandAutocomplete:
		i.Data = discordgo.ApplicationCommandInteractionData{Name: "comando-inexistente"}
	case discordgo.InteractionMessageComponent:
		i.Data = discordgo.MessageComponentInteractionData{CustomID: "custom-inexistente"}
	case discordgo.InteractionModalSubmit:
		// CustomID REAL do /exec com comando vazio: se o handler rodar, responde
		// "Comando vazio." — oraculo observavel (modal desconhecido seria
		// silencioso e nao distinguiria guarda presente de ausente).
		i.Data = discordgo.ModalSubmitInteractionData{CustomID: "exec:main|web"}
	}
	return i
}

func TestOnInteractionNegaIntrusoNosQuatroTipos(t *testing.T) {
	tipos := []discordgo.InteractionType{
		discordgo.InteractionApplicationCommand,
		discordgo.InteractionApplicationCommandAutocomplete,
		discordgo.InteractionMessageComponent,
		discordgo.InteractionModalSubmit,
	}
	for _, tipo := range tipos {
		b, rt := authBot(t)
		b.onInteraction(b.session, interacaoDe("intruso-666", tipo))

		corpo := string(rt.all())
		// Nenhum caminho de handler pode ter rodado; para os tipos com resposta
		// visível, a única chamada permitida é a negação efêmera.
		if strings.Contains(corpo, "comando-inexistente") {
			t.Fatalf("tipo %d: handler rodou para intruso", tipo)
		}
		switch tipo {
		case discordgo.InteractionApplicationCommand, discordgo.InteractionMessageComponent:
			if !strings.Contains(corpo, "permiss") {
				t.Fatalf("tipo %d: intruso não recebeu a negação efêmera (corpo: %.80q)", tipo, corpo)
			}
		default: // autocomplete e modal negam em silêncio
			if len(rt.bodies) != 0 {
				t.Fatalf("tipo %d: esperava silêncio para intruso, houve %d chamada(s)", tipo, len(rt.bodies))
			}
		}
	}
}

func TestOnInteractionDonoPassaDoPortao(t *testing.T) {
	// Contraprova: o dono NÃO recebe a negação — o fluxo segue para o handler
	// (que aqui não acha o comando e não responde nada, mas o ponto é o portão).
	b, rt := authBot(t)
	b.onInteraction(b.session, interacaoDe("dono-123", discordgo.InteractionApplicationCommand))
	if strings.Contains(string(rt.all()), "permiss") {
		t.Fatal("dono recebeu a negação de permissão — portão bloqueando o próprio dono")
	}
}
