package discord

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// newAutocompleteWiringBot monta um Bot mínimo (sem dashboard) com uma
// sessão cuja saída HTTP é interceptada por recordingTransport -- padrão de
// newExecWiringBot (exec_audit_wiring_test.go), aqui sem host de exec.
func newAutocompleteWiringBot(t *testing.T, hosts []*dockerx.Client) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{},
		hosts:   hosts,
		session: session,
	}, rt
}

// autocompleteInteraction monta a interação de autocomplete no formato que
// handleAutocomplete espera: opção "container" (vazia = usuário ainda não
// digitou nada, o caso que hoje mostra 22 locais e no máximo 3 do master).
// Não há helper de autocomplete no pacote antes deste arquivo.
func autocompleteInteraction(typed string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommandAutocomplete,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "exec",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{
					Name:    "container",
					Type:    discordgo.ApplicationCommandOptionString,
					Value:   typed,
					Focused: true,
				},
			},
		},
	}}
}

type autocompletePayload struct {
	Data struct {
		Choices []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"choices"`
	} `json:"data"`
}

// TestHandleAutocompleteDistribuiCotaPorHost prova a fiação: sem cota, um
// host com muitos containers esgota as 25 vagas do Discord sozinho e o outro
// host some do autocomplete -- reproduz em dublê a medição real de produção
// (host local com 22 containers, teto 25, sobravam só 3 vagas para o
// master). manyContainersHost vem de render_collect_wiring_test.go, mesmo
// pacote.
func TestHandleAutocompleteDistribuiCotaPorHost(t *testing.T) {
	hostMain := manyContainersHost(t, "main", "Oracle Main", "local-", 30)
	hostMaster := manyContainersHost(t, "master", "Oracle Master", "remoto-", 5)
	b, rt := newAutocompleteWiringBot(t, []*dockerx.Client{hostMain, hostMaster})

	b.handleAutocomplete(autocompleteInteraction(""))

	var payload autocompletePayload
	if err := json.Unmarshal(rt.all(), &payload); err != nil {
		t.Fatalf("payload nao decodificou: %v; corpo: %s", err, rt.all())
	}

	if got := len(payload.Data.Choices); got > 25 {
		t.Fatalf("esperava no maximo 25 choices (limite do Discord), vieram %d", got)
	}

	hasMaster := false
	for _, c := range payload.Data.Choices {
		if len(c.Value) >= len("master:") && c.Value[:len("master:")] == "master:" {
			hasMaster = true
			break
		}
	}
	if !hasMaster {
		t.Fatalf("esperava pelo menos 1 choice do host master, vieram %d choices, nenhuma com prefixo master: -- %+v", len(payload.Data.Choices), payload.Data.Choices)
	}
}

// TestHandleAutocompleteNaoEmiteValueAcimaDoLimite prova a fiação: um único
// container com nome gigante (>95 chars, dentro do limite de 255 do Docker)
// não pode contaminar a resposta inteira do autocomplete com um choice cujo
// Value estoura o teto de 100 caracteres que o Discord aceita -- isso faz o
// Discord rejeitar a RESPOSTA COMPLETA, e não só o item, apagando o
// autocomplete de containers normais no mesmo host e nos demais.
func TestHandleAutocompleteNaoEmiteValueAcimaDoLimite(t *testing.T) {
	hostMain := manyContainersHost(t, "main", "Oracle Main", strings.Repeat("c", 140), 1)
	hostMaster := manyContainersHost(t, "master", "Oracle Master", "remoto-", 3)
	b, rt := newAutocompleteWiringBot(t, []*dockerx.Client{hostMain, hostMaster})

	b.handleAutocomplete(autocompleteInteraction(""))

	var payload autocompletePayload
	if err := json.Unmarshal(rt.all(), &payload); err != nil {
		t.Fatalf("payload nao decodificou: %v; corpo: %s", err, rt.all())
	}

	for _, c := range payload.Data.Choices {
		if n := len([]rune(c.Value)); n > 100 {
			t.Fatalf("choice com Value de %d runes (> limite de 100 do Discord): %q", n, c.Value)
		}
	}

	masterCount := 0
	for _, c := range payload.Data.Choices {
		if strings.HasPrefix(c.Value, "master:") {
			masterCount++
		}
	}
	if masterCount != 3 {
		t.Fatalf("esperava os 3 containers do host master presentes (prova que o nome gigante foi OMITIDO sem contaminar o resto), vieram %d -- %+v", masterCount, payload.Data.Choices)
	}
}
