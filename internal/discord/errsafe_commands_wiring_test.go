package discord

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// Este arquivo cobre os 4 sites de commands.go que a branch
// auto/20260919-errsafe-5-sites trocou para errSafe e deixou sem teste
// (select_credencial_vazamento_test.go só cobre o site de components.go).
// Escrito na drenagem de 25/09/2026. Em todos, a falha simulada é de
// TRANSPORTE — o único caminho em que o erro do discordgo imprime a URL
// (que carrega o token da interação); 403/*RESTError não vaza e não provaria
// nada.

// callbackNetErrTransport derruba com erro de transporte o POST de callback
// de interação (/interactions/<id>/<token>/callback) e delega o resto ao
// recordingTransport embutido (inclusive o knob netErrEdit do PATCH).
type callbackNetErrTransport struct {
	*recordingTransport
}

func (t callbackNetErrTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/callback") {
		return nil, errors.New("read: connection reset by peer")
	}
	return t.recordingTransport.RoundTrip(req)
}

func slashInteraction(name string, opts ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		ID:   "1", AppID: "2", Token: tokenDaInteracao, ChannelID: "555",
		Data: discordgo.ApplicationCommandInteractionData{Name: name, Options: opts},
	}}
}

// capturaLog roda fn com o log redirecionado e devolve o que foi escrito.
func capturaLog(fn func()) string {
	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	fn()
	return buf.String()
}

func exigeLogSemToken(t *testing.T, saida, marca string) {
	t.Helper()
	// Contraprova positiva: o caminho de erro rodou e logou (senão a asserção
	// de não-vazamento passaria com o log simplesmente ausente).
	if !strings.Contains(saida, marca) {
		t.Fatalf("nada logado com %q — o caminho de erro não foi exercitado: %q", marca, saida)
	}
	if strings.Contains(saida, tokenDaInteracao) {
		t.Fatalf("credencial da interação vazou no log (%s): %q", marca, saida)
	}
}

func TestCmdStatusFalhaNoDeferNaoVazaCredencial(t *testing.T) {
	b, rt := newWiringBot(t, "")
	b.session.Client = &http.Client{Transport: callbackNetErrTransport{rt}}
	saida := capturaLog(func() { b.cmdStatus(slashInteraction("status")) })
	exigeLogSemToken(t, saida, "defer status:")
}

func TestCmdStatusFalhaNaEdicaoNaoVazaCredencial(t *testing.T) {
	b, rt := newWiringBot(t, "")
	rt.netErrEdit = true
	saida := capturaLog(func() { b.cmdStatus(slashInteraction("status")) })
	exigeLogSemToken(t, saida, "edit status:")
}

func TestCmdDashboardFalhaNoDeferNaoVazaCredencial(t *testing.T) {
	b, rt := newWiringBot(t, "")
	b.session.Client = &http.Client{Transport: callbackNetErrTransport{rt}}
	saida := capturaLog(func() { b.cmdDashboard(slashInteraction("dashboard")) })
	exigeLogSemToken(t, saida, "defer dashboard:")
}

func TestCmdContainerActionFalhaNoDeferNaoVazaCredencial(t *testing.T) {
	b, rt := newWiringBot(t, "")
	b.session.Client = &http.Client{Transport: callbackNetErrTransport{rt}}
	i := slashInteraction("restart", &discordgo.ApplicationCommandInteractionDataOption{
		Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: "main:web",
	})
	saida := capturaLog(func() { b.cmdContainerAction(i, "restart") })
	exigeLogSemToken(t, saida, "defer restart:")
}
