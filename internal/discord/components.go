package discord

import (
	"context"
	"errors"
	"log"
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

// maxSelectOptions é o limite do Discord para opções de um select menu.
const maxSelectOptions = 25

// hostContainers é a lista de containers de UM host, já coletada — separado
// da chamada de rede (host.List) para que a distribuição de cota entre hosts
// seja uma função pura e testável sem Docker.
type hostContainers struct {
	key, label string
	containers []dockerx.Container
}

// buildSelectOptions distribui os containers de vários hosts em até
// maxSelectOptions opções, em duas passadas: a 1ª garante uma cota justa
// (maxSelectOptions/len(hosts)) para cada host, a 2ª preenche as sobras na
// ordem original dos hosts. Sem isso, um host com muitos containers consome o
// teto sozinho e hosts remotos (menos containers) somem do select.
func buildSelectOptions(hosts []hostContainers, multiHost bool) []discordgo.SelectMenuOption {
	options := make([]discordgo.SelectMenuOption, 0, maxSelectOptions)
	if len(hosts) == 0 {
		return options
	}

	toOption := func(h hostContainers, c dockerx.Container) discordgo.SelectMenuOption {
		label := selectEmoji(c.State) + " " + c.Name
		desc := c.Status
		if multiHost {
			desc = h.label + " · " + c.Status
		}
		return discordgo.SelectMenuOption{
			Label:       truncate(label, 100),
			Value:       truncate(target(h.key, c.Name), 100),
			Description: truncate(desc, 100),
		}
	}

	quota := maxSelectOptions / len(hosts)
	taken := make([]int, len(hosts))

	// passada 1: cota garantida por host.
	for i, h := range hosts {
		for _, c := range h.containers {
			if taken[i] >= quota || len(options) >= maxSelectOptions {
				break
			}
			options = append(options, toOption(h, c))
			taken[i]++
		}
	}

	// passada 2: sobras, na ordem original dos hosts.
	for i, h := range hosts {
		if len(options) >= maxSelectOptions {
			break
		}
		for _, c := range h.containers[taken[i]:] {
			if len(options) >= maxSelectOptions {
				break
			}
			options = append(options, toOption(h, c))
		}
	}

	return options
}

// buildDashboardComponents monta os controles do painel: um select menu com os
// containers de TODOS os hosts e um botão de atualização manual. Lista os
// containers de cada host (2ª listagem do ciclo — dashboardCollect já fez a
// 1ª); mantido para quem só precisa dos componentes sem os embeds.
func (b *Bot) buildDashboardComponents(ctx context.Context) []discordgo.MessageComponent {
	hosts := make([]hostContainers, 0, len(b.hosts))
	for _, host := range b.hosts {
		list, err := host.List(ctx)
		if err != nil {
			log.Printf("buildDashboardComponents %q: %v", host.Key, err)
			continue // host offline: pula suas opções
		}
		hosts = append(hosts, hostContainers{key: host.Key, label: host.Label, containers: list})
	}
	return b.componentsFrom(hosts)
}

// componentsFrom monta os mesmos controles a partir de uma coleta JÁ FEITA
// (ver dashboardCollect) — usada por render() para não listar os containers
// de cada host uma 2ª vez por ciclo.
func (b *Bot) componentsFrom(hosts []hostContainers) []discordgo.MessageComponent {
	multiHost := len(b.hosts) > 1

	options := buildSelectOptions(hosts, multiHost)

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
		b.replyEphemeral(i, "Nenhum container disponível.") // instantâneo: cabe nos 3s, não deferir.
		return
	}

	// Defere JÁ: host.State abaixo é chamada de rede (ContainerInspect via
	// socket-proxy; no host remoto, um `ssh` novo com ConnectTimeout=10s), e o
	// InteractionRespond original era a resposta INICIAL, com janela de 3s do
	// Discord. Mesmo padrão do showLogsEphemeral (ops.go:114-117).
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	hostKey, name := parseTarget(values[0])
	host := b.hostByKey(hostKey)
	if host == nil {
		b.editResponse(i, "❌ Host desconhecido.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	state, err := host.State(ctx, name)
	if err != nil {
		if errors.Is(err, dockerx.ErrNotFound) {
			b.editResponse(i, "❌ Container `"+name+"` não encontrado em "+host.Label+".")
			return
		}
		log.Printf("select state %s/%s: %v", host.Key, name, err)
		b.editResponse(i, "⚠️ Falha ao consultar `"+name+"` em "+host.Label+": "+err.Error())
		return
	}

	content := "**" + name + "** em _" + host.Label + "_ (" + state + ") — escolha uma ação:"
	comps := actionButtons(hostKey, name, state)
	if _, err := b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &content,
		Components: &comps,
	}); err != nil {
		log.Printf("select %s: %v", name, err)
	}
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
		// Mesmo defer do handleConfirm, pelo mesmo motivo: o updateEphemeral usa
		// InteractionResponseUpdateMessage, que é a resposta INICIAL da interação
		// e tem janela de 3s. runActionAudited pode levar até 60s (start de
		// container lento, daemon ocupado), e aí o Discord mostra "This
		// interaction failed" mesmo com a ação concluindo. O stop/restart já
		// tinham sido corrigidos; este ramo (botão de start/pause/unpause)
		// passou batido nas duas correções anteriores.
		_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredMessageUpdate,
		})

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res := b.runActionAudited(ctx, i, hostKey, verb, name)
		empty := []discordgo.MessageComponent{}
		_, _ = b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content:    &res,
			Components: &empty,
		})
		b.dashboard.refreshAfterAction()
	}
}

// runActionAudited aplica rate limit, executa a ação e registra na auditoria.
func (b *Bot) runActionAudited(ctx context.Context, i *discordgo.InteractionCreate, hostKey, verb, name string) string {
	hostLabel := hostKey
	if h := b.hostByKey(hostKey); h != nil {
		hostLabel = h.Label
	}
	if !b.limiter.Allow() {
		b.auditRefusal(auditEntry{actor: actorName(i), action: verb, host: hostLabel, target: name})
		return "⏳ Muitas ações em pouco tempo — aguarde alguns segundos."
	}
	res := b.runAction(ctx, hostKey, verb, name)
	b.audit(auditEntry{actor: actorName(i), action: verb, host: hostLabel, target: name, result: res})
	return res
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

	// Defere JÁ: restart/stop podem levar mais que a janela de 3s do Discord
	// (ex.: container que ignora SIGTERM segura o stop até SHUTDOWN_TIMEOUT).
	// Sem o defer, o Discord mostra "This interaction failed" mesmo com a
	// ação concluindo com sucesso.
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res := b.runActionAudited(ctx, i, p.hostKey, p.verb, p.name)
	empty := []discordgo.MessageComponent{}
	_, _ = b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content:    &res,
		Components: &empty,
	})
	b.dashboard.refreshAfterAction()
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
	case errors.Is(err, dockerx.ErrNotFound):
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
