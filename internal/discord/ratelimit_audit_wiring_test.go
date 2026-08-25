package discord

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO da auditoria de rate limit: a recusa por
// allow-list (execAllowed, ops.go:181) já era auditada, mas a recusa por
// rate limit -- a única auditoria de start/pause/unpause/stop/restart/exec --
// não era. Sem isso, uma rajada de N ações aparece no canal de auditoria como
// só as que passaram, sem sinal das recusadas.

// waitForBodyContains espera até que algum corpo gravado pelo recordingTransport
// contenha substr (a auditoria é assíncrona, ver audit.go:57), ou falha no
// timeout.
func waitForBodyContains(t *testing.T, rt *recordingTransport, substr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.Contains(string(rt.all()), substr) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("corpo esperado não apareceu em 2s: %q (recebido: %.200q)", substr, string(rt.all()))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func rateLimitedActionBot(t *testing.T) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}

	b := &Bot{
		cfg:     &config.Config{AuditChannelID: "999"},
		hosts:   []*dockerx.Client{fakeDockerHost(t, "")},
		session: session,
		limiter: newRateLimiter(0, 0), // Allow() sempre false: todo clique é recusado
	}
	// O ramo default de handleAction termina em refreshAfterAction, que precisa
	// de um dashboard não-nil mesmo quando a ação é recusada (o refresh roda
	// incondicionalmente depois da resposta). Ver STATE do projeto.
	b.dashboard = newDashboard(b)
	return b, rt
}

func actionInteraction(customID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func execModalInteraction(hostKey, name, cmd string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		ID:   "1", AppID: "2", Token: "tok",
		Data: discordgo.ModalSubmitInteractionData{
			CustomID: "exec:" + target(hostKey, name),
			Components: []discordgo.MessageComponent{
				&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					&discordgo.TextInput{CustomID: "cmd", Value: cmd},
				}},
			},
		},
	}}
}

// TestHandleActionAuditaRecusaPorRateLimit prova que start/pause/unpause,
// quando recusados por rate limit, deixam rastro no canal de auditoria --
// antes desta mudança, a recusa era só a resposta efêmera ao usuário; o canal
// (único registro durável da superfície perigosa) ficava mudo.
func TestHandleActionAuditaRecusaPorRateLimit(t *testing.T) {
	b, rt := rateLimitedActionBot(t)
	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")

	waitForBodyContains(t, rt, "rate limit")
}

// TestHandleConfirmAuditaRecusaPorRateLimit é o espelho para o caminho
// pós-confirmação (stop/restart).
func TestHandleConfirmAuditaRecusaPorRateLimit(t *testing.T) {
	b, rt := rateLimitedActionBot(t)
	b.confirms = newConfirmManager(b)
	token := b.confirms.add("stop", "main", "web", actionInteraction("act:stop:main:web").Interaction)

	b.handleConfirm(actionInteraction("cfm:ok:"+token), "cfm:ok:"+token)

	waitForBodyContains(t, rt, "rate limit")
}

// TestHandleModalExecAuditaRecusaPorRateLimit é o terceiro caminho que passa
// por rate limit: o /exec.
func TestHandleModalExecAuditaRecusaPorRateLimit(t *testing.T) {
	b, rt := rateLimitedActionBot(t)
	b.handleModal(execModalInteraction("main", "web", "ls -la"))

	waitForBodyContains(t, rt, "rate limit")
}

// TestHandleActionSemRateLimitNaoAuditaComoRecusa é a contraprova: com o
// limiter liberando (o caso normal), a auditoria da ação bem-sucedida não
// pode conter "rate limit" -- sem isso, um teste que auditasse tudo com o
// rótulo fixo "rate limit" passaria pelos 3 testes acima sem checar nada.
func TestHandleActionSemRateLimitNaoAuditaComoRecusa(t *testing.T) {
	b, rt := rateLimitedActionBot(t)
	b.limiter = newRateLimiter(8, 0.5) // Allow() sempre true na 1ª chamada

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")

	waitForBodyContains(t, rt, "iniciado") // aguarda a auditoria (assíncrona) do sucesso sair
	if strings.Contains(string(rt.all()), "rate limit") {
		t.Fatal("ação permitida pelo limiter foi auditada como recusa por rate limit")
	}
}
