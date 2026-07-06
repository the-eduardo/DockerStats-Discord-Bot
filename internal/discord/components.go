package discord

import (
	"context"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// CustomIDs dos componentes. Usamos ":" como separador — caractere que nem os
// nomes de container nem as Keys de host contêm, então o parsing é seguro.
const (
	idSelect  = "dash:select"
	idRefresh = "dash:refresh"
	prefixAct = "act:" // act:<verbo>:<hostKey>:<container>
	prefixCfm = "cfm:" // cfm:<ok|no>:<token>
)

// target codifica host+container no valor de um componente ("hostKey:container").
func target(hostKey, name string) string { return hostKey + ":" + name }

// parseTarget separa "hostKey:container". Sem ":", assume host local ("").
func parseTarget(v string) (hostKey, name string) {
	if k, n, ok := strings.Cut(v, ":"); ok {
		return k, n
	}
	return "", v
}

// buildDashboardComponents monta os controles do painel: um select menu com os
// containers de TODOS os hosts e um botão de atualização manual.
func (b *Bot) buildDashboardComponents(ctx context.Context) []discordgo.MessageComponent {
	multiHost := len(b.hosts) > 1

	options := make([]discordgo.SelectMenuOption, 0, 25)
	for _, host := range b.hosts {
		list, err := host.List(ctx)
		if err != nil {
			continue // host offline: pula suas opções
		}
		for _, c := range list {
			if len(options) == 25 { // limite do Discord
				break
			}
			label := selectEmoji(c.State) + " " + c.Name
			desc := c.Status
			if multiHost {
				desc = host.Label + " · " + c.Status
			}
			options = append(options, discordgo.SelectMenuOption{
				Label:       truncate(label, 100),
				Value:       truncate(target(host.Key, c.Name), 100),
				Description: truncate(desc, 100),
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
// O customID inclui a Key do host: act:<verbo>:<hostKey>:<container>.
func actionButtons(hostKey, name, state string) []discordgo.MessageComponent {
	t := target(hostKey, name)
	var primary []discordgo.MessageComponent
	switch state {
	case "running":
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "🔄 Reiniciar", Style: discordgo.PrimaryButton, CustomID: prefixAct + "restart:" + t},
			discordgo.Button{Label: "⏸️ Pausar", Style: discordgo.SecondaryButton, CustomID: prefixAct + "pause:" + t},
			discordgo.Button{Label: "⏹️ Parar", Style: discordgo.DangerButton, CustomID: prefixAct + "stop:" + t},
		}
	case "paused":
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "▶️ Retomar", Style: discordgo.SuccessButton, CustomID: prefixAct + "unpause:" + t},
			discordgo.Button{Label: "⏹️ Parar", Style: discordgo.DangerButton, CustomID: prefixAct + "stop:" + t},
		}
	default: // exited, created, dead...
		primary = []discordgo.MessageComponent{
			discordgo.Button{Label: "▶️ Iniciar", Style: discordgo.SuccessButton, CustomID: prefixAct + "start:" + t},
		}
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: primary},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "📜 Logs", Style: discordgo.SecondaryButton, CustomID: prefixAct + "logs:" + t},
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
	hostKey, name := parseTarget(values[0])
	host := b.hostByKey(hostKey)
	if host == nil {
		b.replyEphemeral(i, "❌ Host desconhecido.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	state, err := host.State(ctx, name)
	if err != nil {
		b.replyEphemeral(i, "❌ Container `"+name+"` não encontrado em "+host.Label+".")
		return
	}

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:      discordgo.MessageFlagsEphemeral,
			Content:    "**" + name + "** em _" + host.Label + "_ (" + state + ") — escolha uma ação:",
			Components: actionButtons(hostKey, name, state),
		},
	})
}

// handleAction trata o clique num botão de ação. Ações destrutivas (parar,
// reiniciar) passam por confirmação; as demais executam direto.
func (b *Bot) handleAction(i *discordgo.InteractionCreate, customID string) {
	// Formato: act:<verbo>:<hostKey>:<container>. Nem verbo, nem hostKey, nem
	// nome contêm ":", então SplitN(4) separa corretamente.
	parts := strings.SplitN(customID, ":", 4)
	if len(parts) != 4 {
		return
	}
	verb, hostKey, name := parts[1], parts[2], parts[3]

	switch verb {
	case "stop", "restart":
		b.startConfirm(i, verb, hostKey, name)
	case "logs":
		b.showLogsEphemeral(i, hostKey, name)
	default: // start, pause, unpause
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		b.updateEphemeral(i, b.runAction(ctx, hostKey, verb, name))
		b.dashboard.refreshNow()
	}
}

// startConfirm troca a mensagem efêmera pelos botões de confirmação.
func (b *Bot) startConfirm(i *discordgo.InteractionCreate, verb, hostKey, name string) {
	label := "parada"
	if verb == "restart" {
		label = "reinício"
	}
	token := b.confirms.add(verb, hostKey, name, i.Interaction)

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
	b.updateEphemeral(i, b.runAction(ctx, p.hostKey, p.verb, p.name))
	b.dashboard.refreshNow()
}

// runAction executa a operação de ciclo de vida no host indicado e devolve a
// mensagem de resultado. Reutilizada pelos botões, confirmação e slash commands.
func (b *Bot) runAction(ctx context.Context, hostKey, verb, name string) string {
	host := b.hostByKey(hostKey)
	if host == nil {
		return "❌ Host desconhecido."
	}
	timeout := int(b.cfg.ShutdownTimeout.Seconds())

	var err error
	var done string
	switch verb {
	case "start":
		err, done = host.Start(ctx, name), "iniciado"
	case "restart":
		err, done = host.Restart(ctx, name, timeout), "reiniciado"
	case "stop":
		err, done = host.Stop(ctx, name, timeout), "parado"
	case "pause":
		err, done = host.Pause(ctx, name), "pausado"
	case "unpause":
		err, done = host.Unpause(ctx, name), "retomado"
	default:
		return "Ação desconhecida: " + verb
	}

	switch {
	case err == dockerx.ErrNotFound:
		return "❌ Container `" + name + "` não encontrado em " + host.Label + "."
	case err != nil:
		return "⚠️ Erro ao " + verb + " `" + name + "` em " + host.Label + ": " + err.Error()
	default:
		return "✅ `" + name + "` " + done + " em " + host.Label + "."
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
