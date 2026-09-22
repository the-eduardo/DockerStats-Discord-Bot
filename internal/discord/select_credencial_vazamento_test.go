package discord

import (
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do errSafe em handleSelect: uma falha de
// TRANSPORTE (não 4xx/5xx) no PATCH de InteractionResponseEdit volta como
// *url.Error, cujo Error() imprime a URL inteira — e essa URL carrega o token
// da interação (webhooks/<app>/<token>/messages/@original). Logar esse erro
// cru vazaria uma credencial de 15 min no stdout do container (que o alloy
// coleta para o Loki). O failEdit (403/*RESTError) NÃO vaza — por isso o
// knob usado aqui é netErrEdit, não failEdit.
func TestHandleSelectNaoVazaCredencialNoLog(t *testing.T) {
	rt := &recordingTransport{netErrEdit: true}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	host, _ := selectFakeHost(t, rt)
	b := &Bot{hosts: []*dockerx.Client{host}, session: session}

	i := selectInteraction("main:web")
	// selectInteraction usa Token "tok", que casa por acidente dentro de
	// "token-de-teste" e tornaria a asserção de não-vazamento um falso-verde
	// permanente (armadilha documentada em ops_wiring_test.go). Sobrescrever
	// com uma credencial que não aparece em nenhum outro lugar do fluxo.
	i.Interaction.Token = tokenDaInteracao

	var buf strings.Builder
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b.handleSelect(i)

	if strings.Contains(buf.String(), tokenDaInteracao) {
		t.Fatalf("credencial da interação vazou no log: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "select ") {
		t.Fatalf("nada foi logado — o teste não exercitou o caminho de erro: %s", buf.String())
	}
}
