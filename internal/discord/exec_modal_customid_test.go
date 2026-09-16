package discord

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// Este arquivo testa a FIAÇÃO de cmdExec (ops.go), não uma função pura: o
// custom_id do modal de /exec era texto livre ("exec:"+hostKey+":"+name, sem
// MaxLength) e o Discord recusa custom_id de modal acima de 100 caracteres
// com um 400 silencioso — o handler descartava esse erro (`_ =`), então o
// dono via só "This interaction failed" e nada no log. Ver proposta do
// especialista de 14/09/2026.

// execCommandInteraction monta a interação de slash command /exec, no
// formato que cmdExec espera (opção "container" como texto livre — o mesmo
// shape de logsInteraction em ops_wiring_test.go, adaptado ao comando).
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

func TestCmdExecRecusaAlvoLongoDemais(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session}

	// "main:" + 120 'a' ultrapassa os 100 chars do custom_id do Discord.
	b.cmdExec(execCommandInteraction("main:" + strings.Repeat("a", 120)))

	sent := string(rt.all())
	if strings.Contains(sent, `"type":9`) {
		t.Fatalf("cmdExec abriu modal com custom_id acima do teto do Discord: %q", sent)
	}
	if !strings.Contains(sent, `"type":4`) {
		t.Fatalf("cmdExec não respondeu com mensagem efêmera de recusa: %q", sent)
	}
}

func TestCmdExecAbreModalComNomeNormal(t *testing.T) {
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session}

	b.cmdExec(execCommandInteraction("main:Docker-StatusBot"))

	sent := string(rt.all())
	if !strings.Contains(sent, `"type":9`) {
		t.Fatalf("cmdExec não abriu modal para alvo dentro do teto: %q", sent)
	}
	if !strings.Contains(sent, `"custom_id":"exec:main:Docker-StatusBot"`) {
		t.Fatalf("modal não carregou o custom_id esperado: %q", sent)
	}
}

func TestCmdExecLogaFalhaDeAberturaDeModal(t *testing.T) {
	rt := &recordingTransport{failCallback: true}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{session: session}

	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	b.cmdExec(execCommandInteraction("main:web"))

	out := buf.String()
	if !strings.Contains(out, "exec modal") {
		t.Fatalf("falha na abertura do modal não foi logada: %q", out)
	}
	if strings.Contains(out, tokenDaInteracao) {
		t.Fatalf("log vazou o token da interação: %q", out)
	}
}

// TestMaxCustomIDValorDocumentado ancora o teto na constante que o Discord
// exige (100), evitando que uma constante frouxa passe os testes acima sem
// de fato refletir o limite real da API.
func TestMaxCustomIDValorDocumentado(t *testing.T) {
	if maxCustomID != 100 {
		t.Fatalf("maxCustomID = %s, quer 100 (teto oficial de custom_id do Discord)", strconv.Itoa(maxCustomID))
	}
}
