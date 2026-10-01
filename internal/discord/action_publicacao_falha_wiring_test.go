package discord

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Este arquivo testa a FIAÇÃO da publicação do resultado das ações de ciclo
// de vida (start/pause/unpause via handleAction, stop/restart via
// handleConfirm, e start/stop/restart/pause/unpause via slash em
// cmdContainerAction): os 3 pontos descartavam o erro da publicação final
// (`_, _ = b.session.InteractionResponseEdit(...)` em components.go, ou o
// retorno de editResponse ignorado em commands.go:244) sem deixar rastro
// algum -- nem no log, nem na auditoria, que já saiu DENTRO de
// runActionAudited antes deste edit. Quando o PATCH falha, a ação já rodou
// no container, mas o clique fica pendurado e os botões não são limpos.
// Achado do enxame, 01/10/2026. errsafe_commands_wiring_test.go (25/09/2026)
// já cobre a falha no DEFER de cmdContainerAction ("defer restart:"); falta
// a falha na publicação do RESULTADO ("ação restart:"), que é o caso novo
// aqui -- confirmado por mutação que a suíte ficava verde sem essa cobertura.

// TestHandleActionLogaFalhaDePublicacao prova o caso (1) da proposta: com o
// PATCH de @original recusado (403, *RESTError que NÃO vaza), handleAction
// (ramo start/pause/unpause) precisa logar a falha.
func TestHandleActionLogaFalhaDePublicacao(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b, rt := newActionWiringBot(t)
	rt.failEdit = true

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	esperaAuditoria(&b.dashboard.renderWG, 2*time.Second)

	if !strings.Contains(buf.String(), `ação start "web"`) {
		t.Fatalf("PATCH falhou mas o log não registrou a publicação perdida: %q", buf.String())
	}
}

// TestHandleConfirmLogaFalhaDePublicacao é o caso (2): o mesmo defeito no
// caminho pós-confirmação (stop/restart), que é o mais perigoso por lidar
// com as ações destrutivas.
func TestHandleConfirmLogaFalhaDePublicacao(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b, rt := newActionWiringBot(t)
	rt.failEdit = true

	token := b.confirms.add("stop", "main", "web", actionInteraction("act:stop:main:web").Interaction)
	b.handleConfirm(actionInteraction("cfm:ok:"+token), "cfm:ok:"+token)
	esperaAuditoria(&b.dashboard.renderWG, 2*time.Second)

	if !strings.Contains(buf.String(), `ação stop "web"`) {
		t.Fatalf("PATCH falhou mas o log não registrou a publicação perdida: %q", buf.String())
	}
}

// TestHandleActionNaoVazaTokenAoLogarFalhaDeTransporte é a contraprova de
// redação do caso (3): o 403 de failEdit vem como *RESTError e não carrega o
// token; só a falha de TRANSPORTE (netErrEdit) devolve um *url.Error que
// imprime a URL de @original inteira, com o token da interação dentro. Sem
// este caso, trocar errSafe(i, err) por err.Error() no helper não quebraria
// teste algum.
func TestHandleActionNaoVazaTokenAoLogarFalhaDeTransporte(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b, rt := newActionWiringBot(t)
	rt.netErrEdit = true

	i := actionInteraction("act:start:main:web")
	i.Interaction.Token = tokenDaInteracao // "tok" (default) é curto demais p/ asserir vazamento

	b.handleAction(i, "act:start:main:web")
	esperaAuditoria(&b.dashboard.renderWG, 2*time.Second)

	logado := buf.String()
	if !strings.Contains(logado, `ação start "web"`) {
		t.Fatalf("falha de transporte no PATCH deveria logar a publicação perdida: %q", logado)
	}
	if strings.Contains(logado, tokenDaInteracao) {
		t.Fatalf("token da interação vazou no log: %q", logado)
	}
}

// TestHandleActionNaoLogaNadaQuandoPublicacaoFunciona é o controle negativo
// do caso (4): sem failEdit/netErrEdit (PATCH responde 200, como o
// recordingTransport embutido faz por padrão), nenhuma linha com o prefixo
// "ação " deve aparecer.
func TestHandleActionNaoLogaNadaQuandoPublicacaoFunciona(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b, _ := newActionWiringBot(t)
	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	esperaAuditoria(&b.dashboard.renderWG, 2*time.Second)

	if strings.Contains(buf.String(), "ação ") {
		t.Fatalf("PATCH funcionou (sem failEdit/netErrEdit) mas apareceu log de publicação perdida: %q", buf.String())
	}
}

// TestCmdContainerActionLogaFalhaNaPublicacaoDoResultado cobre o 3º site: o
// slash /restart (cmdContainerAction) ignorava o erro de editResponse ao
// publicar o RESULTADO de runActionAudited -- distinto do defer, já coberto
// por TestCmdContainerActionFalhaNoDeferNaoVazaCredencial.
func TestCmdContainerActionLogaFalhaNaPublicacaoDoResultado(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	b, rt := newActionWiringBot(t)
	rt.netErrEdit = true
	i := slashInteraction("restart", &discordgo.ApplicationCommandInteractionDataOption{
		Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: "main:web",
	})

	b.cmdContainerAction(i, "restart")

	logado := buf.String()
	if !strings.Contains(logado, `ação restart "web"`) {
		t.Fatalf("PATCH do resultado falhou mas o log não registrou a publicação perdida: %q", logado)
	}
	if strings.Contains(logado, tokenDaInteracao) {
		t.Fatalf("token da interação vazou no log: %q", logado)
	}
}
