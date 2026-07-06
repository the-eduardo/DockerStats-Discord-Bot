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

// actionButtons devolve os botões de ação para um container específico.
func actionButtons(name string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "▶️ Iniciar", Style: discordgo.SuccessButton, CustomID: prefixAct + "start:" + name},
			discordgo.Button{Label: "🔄 Reiniciar", Style: discordgo.PrimaryButton, CustomID: prefixAct + "restart:" + name},
			discordgo.Button{Label: "⏹️ Parar", Style: discordgo.DangerButton, CustomID: prefixAct + "stop:" + name},
		}},
	}
}

// onComponent roteia interações de componentes (select menu e botões).
func (b *Bot) onComponent(i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	switch {
	case id == idRefresh:
		b.handleRefresh(i)
	case id == idSelect:
		b.handleSelect(i)
	case strings.HasPrefix(id, prefixAct):
		b.handleAction(i, id)
	}
}

// handleRefresh confirma a interação (sem alterar nada por si só) e força um
// render imediato do painel via bot.
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

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Content:    "Container **" + name + "** — escolha uma ação:",
			Components: actionButtons(name),
		},
	})
}

// handleAction executa start/restart/stop a partir do customID do botão e
// atualiza a mensagem efêmera com o resultado, além de re-renderizar o painel.
func (b *Bot) handleAction(i *discordgo.InteractionCreate, customID string) {
	// Formato: act:<verbo>:<container>. Nomes não contêm ":", então SplitN(3) basta.
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 {
		return
	}
	verb, name := parts[1], parts[2]

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
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
	default:
		return
	}

	var msg string
	switch {
	case err == dockerx.ErrNotFound:
		msg = "❌ Container `" + name + "` não encontrado."
	case err != nil:
		msg = "⚠️ Erro ao " + verb + " `" + name + "`: " + err.Error()
	default:
		msg = "✅ Container `" + name + "` " + done + "."
	}

	// Atualiza a mensagem efêmera removendo os botões (evita clique repetido).
	empty := []discordgo.MessageComponent{}
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{Content: msg, Components: empty},
	})

	b.dashboard.refreshNow() // reflete o novo estado no painel
}
