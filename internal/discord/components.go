package discord

import (
	"context"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// CustomIDs dos componentes. Usamos ":" como separador — caractere que nomes de
// container do Docker não podem conter, então não há ambiguidade no parsing.
const (
	idSelect  = "dash:select"
	idRefresh = "dash:refresh"
	prefixAct = "act:" // act:<verbo>:<container>
	prefixCfm = "cfm:" // cfm:<ok|no>:<token>
)

// buildDashboardComponents monta os controles do painel: um select menu com os
// containers e um botão de atualização manual.
func (b *Bot) buildDashboardComponents(ctx context.Context) []discordgo.MessageComponent {
	list, err := b.dx.List(ctx)

	options := make([]discordgo.SelectMenuOption, 0, 25)
	if err == nil {
		for _, c := range list {
			if len(options) == 25 { // limite do Discord
				break
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       truncate(selectEmoji(c.State)+" "+c.Name, 100),
				Value:       truncate(c.Name, 100),
				Description: truncate(c.Status, 100),
			})
		}
	}

	// O select menu não aceita lista vazia; oferecemos um placeholder inerte.
	disabled := false
	if len(options) == 0 {
		disabled = true
		options = append(options, discordgo.SelectMenuOption{Label: "nenhum container", Value: "_none"})
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    idSelect,
				Placeholder: "⚙️ Gerenciar um container…",
				Options:     options,
				Disabled:    disabled,
			},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "🔄 Atualizar agora",
				Style:    discordgo.SecondaryButton,
				CustomID: idRefresh,
			},
		}},
	}
}

// actionButtons devolve os botões de ação adequados ao estado do container.
func actionButtons(name, state string) []discordgo.MessageComponent {
	var primary []discordgo.MessageComponent
	switch state {
	case "running":
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "🔄 Reiniciar", Style: discordgo.PrimaryButton, CustomID: prefixAct + "restart:" + name},
			discordgo.Button{Label: "⏸️ Pausar", Style: discordgo.SecondaryButton, CustomID: prefixAct + "pause:" + name},
			discordgo.Button{Label: "⏹️ Parar", Style: discordgo.DangerButton, CustomID: prefixAct + "stop:" + name},
		}
	case "paused":
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "▶️ Retomar", Style: discordgo.SuccessButton, CustomID: prefixAct + "unpause:" + name},
			discordgo.Button{Label: "⏹️ Parar", Style: discordgo.DangerButton, CustomID: prefixAct + "stop:" + name},
		}
	default: // exited, created, dead...
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "▶️ Iniciar", Style: discordgo.SuccessButton, CustomID: prefixAct + "start:" + name},
		}
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: primary},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "📜 Logs", Style: discordgo.SecondaryButton, CustomID: prefixAct + "logs:" + name},
		}},
	}
}

// onComponent roteia interações de componentes (select menu, botões, confirmação).
func (b *Bot) onComponent(i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	switch {
	case id == idRefresh:
		b.handleRefresh(i)
	case id == idSelect:
		b.handleSelect(i)
	case strings.HasPrefix(id, prefixCfm):
		b.handleConfirm(i, id)
	case strings.HasPrefix(id, prefixAct):
		b.handleAction(i, id)
	}
}

// handleRefresh confirma a interação e força um render imediato do painel.
func (b *Bot) handleRefresh(i *discordgo.InteractionCreate) {
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
	b.dashboard.refreshNow()
}

// handleSelect responde (efêmero) com os botões de ação do container escolhido.
func (b *Bot) handleSelect(i *discordgo.InteractionCreate) {
	values := i.MessageComponentData().Values
	if len(values) == 0 || values[0] == "_none" {
		b.replyEphemeral(i, "Nenhum container disponível.")
		return
	}
	name := values[0]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := b.dx.State(ctx, name)
	if err != nil {
		b.replyEphemeral(i, "❌ Container `"+name+"` não encontrado.")
		return
	}

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Content:    "Container **" + name + "** (" + state + ") — escolha uma ação:",
			Components: actionButtons(name, state),
		},
	})
}

// handleAction trata o clique num botão de ação. Ações destrutivas (parar,
// reiniciar) passam por confirmação; as demais executam direto.
func (b *Bot) handleAction(i *discordgo.InteractionCreate, customID string) {
	// Formato: act:<verbo>:<container>. Nomes não contêm ":", então SplitN(3) basta.
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 {
		return
	}
	verb, name := parts[1], parts[2]

	switch verb {
	case "stop", "restart":
		b.startConfirm(i, verb, name)
	case "logs":
		b.showLogsEphemeral(i, name)
	default: // start, pause, unpause
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		b.updateEphemeral(i, b.runAction(ctx, verb, name))
		b.dashboard.refreshNow()
	}
}

// startConfirm troca a mensagem efêmera pelos botões de confirmação.
func (b *Bot) startConfirm(i *discordgo.InteractionCreate, verb, name string) {
	label := "parada"
	if verb == "restart" {
		label = "reinício"
	}
	token := b.confirms.add(verb, name, i.Interaction)

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: "⚠️ Confirmar **" + label + "** de `" + name + "`? (expira em 30s)",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "✅ Confirmar", Style: discordgo.DangerButton, CustomID: prefixCfm + "ok:" + token},
					discordgo.Button{Label: "✖️ Cancelar", Style: discordgo.SecondaryButton, CustomID: prefixCfm + "no:" + token},
				}},
			},
		},
	})
}

// handleConfirm executa (ou cancela) a ação após a confirmação.
func (b *Bot) handleConfirm(i *discordgo.InteractionCreate, customID string) {
	parts := strings.SplitN(customID, ":", 3) // cfm:<ok|no>:<token>
	if len(parts) != 3 {
		return
	}
	decision, token := parts[1], parts[2]

	p, ok := b.confirms.pop(token)
	if !ok {
		b.updateEphemeral(i, "⌛ Confirmação expirada.")
		return
	}
	if decision == "no" {
		b.updateEphemeral(i, "✖️ `"+p.name+"` — ação cancelada.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	b.updateEphemeral(i, b.runAction(ctx, p.verb, p.name))
	b.dashboard.refreshNow()
}

// runAction executa a operação de ciclo de vida e devolve a mensagem de resultado.
// Reutilizada pelos botões, pela confirmação e pelos slash commands.
func (b *Bot) runAction(ctx context.Context, verb, name string) string {
	timeout := int(b.cfg.ShutdownTimeout.Seconds())

	var err error
	var done string
	switch verb {
	case "start":
		err, done = b.dx.Start(ctx, name), "iniciado"
	case "restart":
		err, done = b.dx.Restart(ctx, name, timeout), "reiniciado"
	case "stop":
		err, done = b.dx.Stop(ctx, name, timeout), "parado"
	case "pause":
		err, done = b.dx.Pause(ctx, name), "pausado"
	case "unpause":
		err, done = b.dx.Unpause(ctx, name), "retomado"
	default:
		return "Ação desconhecida: " + verb
	}

	switch {
	case err == dockerx.ErrNotFound:
		return "❌ Container `" + name + "` não encontrado."
	case err != nil:
		return "⚠️ Erro ao " + verb + " `" + name + "`: " + err.Error()
	default:
		return "✅ Container `" + name + "` " + done + "."
	}
}

// updateEphemeral edita a mensagem efêmera atual, removendo seus componentes.
func (b *Bot) updateEphemeral(i *discordgo.InteractionCreate, content string) {
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: []discordgo.MessageComponent{},
		},
	})
}
