package discord

import (
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// Este arquivo testa a FIAÇÃO do log de falha ao abrir o modal do /exec:
// cmdExec descartava o erro do InteractionRespond (POST de callback) em
// silêncio. netErrCallback simula uma falha de TRANSPORTE nesse POST — o
// caminho que, sem errSafe, vazaria o token da interação (a URL do callback
// carrega o token, e um *url.Error de falha de rede imprime a URL inteira).

func execCommandInteraction(container string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		ID:   "1", AppID: "2", Token: tokenDaInteracao,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "exec",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: container},
			},
		},
	}}
}

func TestCmdExecLogaFalhaAoAbrirModalSemVazarCredencial(t *testing.T) {
	rt := &recordingTransport{netErrCallback: true}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{cfg: &config.Config{}, session: session}

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b.cmdExec(execCommandInteraction("web"))

	if !strings.Contains(buf.String(), "modal exec") {
		t.Fatalf("a falha ao abrir o modal não foi logada: %q", buf.String())
	}
	if strings.Contains(buf.String(), tokenDaInteracao) {
		t.Fatalf("credencial da interação vazou no log: %q", buf.String())
	}
}
