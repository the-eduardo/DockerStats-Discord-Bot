package discord

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// noPerm = 0 → por padrão nenhum membro vê os comandos (só admins/owner).
// Combinado com a checagem de OwnerID, garante o uso privado.
var noPerm int64 = 0

// commandDefs descreve os slash commands da Fase 1.
func commandDefs() []*discordgo.ApplicationCommand {
	containerOpt := &discordgo.ApplicationCommandOption{
		Type:         discordgo.ApplicationCommandOptionString,
		Name:         "container",
		Description:  "Nome do container",
		Required:     true,
		Autocomplete: true,
	}
	return []*discordgo.ApplicationCommand{
		{
			Name:                     "status",
			Description:              "Mostra CPU/RAM/disco do host e o estado dos containers",
			DefaultMemberPermissions: &noPerm,
		},
		{
			Name:                     "dashboard",
			Description:              "Fixa neste canal o painel auto-atualizável de status e controle",
			DefaultMemberPermissions: &noPerm,
		},
		{
			Name:                     "start",
			Description:              "Inicia um container",
			DefaultMemberPermissions: &noPerm,
			Options:                  []*discordgo.ApplicationCommandOption{containerOpt},
		},
		{
			Name:                     "stop",
			Description:              "Para um container (graceful)",
			DefaultMemberPermissions: &noPerm,
			Options:                  []*discordgo.ApplicationCommandOption{containerOpt},
		},
		{
			Name:                     "restart",
			Description:              "Reinicia um container",
			DefaultMemberPermissions: &noPerm,
			Options:                  []*discordgo.ApplicationCommandOption{containerOpt},
		},
	}
}

// registerCommands publica os comandos. Se GuildID estiver setado, registra no
// servidor (efeito imediato); caso contrário, registra globalmente (pode levar
// até ~1h para propagar).
func (b *Bot) registerCommands() error {
	guild := b.cfg.GuildID
	defs := commandDefs()
	b.registered = make([]*discordgo.ApplicationCommand, 0, len(defs))
	for _, cmd := range defs {
		created, err := b.session.ApplicationCommandCreate(b.session.State.User.ID, guild, cmd)
		if err != nil {
			return err
		}
		b.registered = append(b.registered, created)
	}
	scope := "globalmente"
	if guild != "" {
		scope = "no servidor " + guild
	}
	log.Printf("%d slash commands registrados %s", len(b.registered), scope)
	return nil
}

// unregisterCommands limpa os comandos registrados no shutdown.
func (b *Bot) unregisterCommands() {
	for _, cmd := range b.registered {
		if err := b.session.ApplicationCommandDelete(b.session.State.User.ID, b.cfg.GuildID, cmd.ID); err != nil {
			log.Printf("erro ao remover comando %q: %v", cmd.Name, err)
		}
	}
}

// onInteraction roteia interações (comandos e autocomplete).
func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if !b.isOwner(i) {
			b.replyEphemeral(i, "⛔ Você não tem permissão para usar este bot.")
			return
		}
		b.handleCommand(i)
	case discordgo.InteractionApplicationCommandAutocomplete:
		if !b.isOwner(i) {
			return
		}
		b.handleAutocomplete(i)
	case discordgo.InteractionMessageComponent:
		if !b.isOwner(i) {
			b.replyEphemeral(i, "⛔ Você não tem permissão para usar este bot.")
			return
		}
		b.onComponent(i)
	}
}

// handleCommand executa o comando aplicável.
func (b *Bot) handleCommand(i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	switch data.Name {
	case "status":
		b.cmdStatus(i)
	case "dashboard":
		b.cmdDashboard(i)
	case "start":
		b.cmdContainerAction(i, "start")
	case "stop":
		b.cmdContainerAction(i, "stop")
	case "restart":
		b.cmdContainerAction(i, "restart")
	}
}

// cmdStatus responde com o embed do dashboard.
func (b *Bot) cmdStatus(i *discordgo.InteractionCreate) {
	// Coleta de stats pode levar ~1s: adia a resposta para não estourar o
	// timeout de 3s do Discord.
	if err := b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("defer status: %v", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	embed := b.buildDashboardEmbed(ctx)

	if _, err := b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	}); err != nil {
		log.Printf("edit status: %v", err)
	}
}

// cmdDashboard fixa o painel persistente no canal onde o comando foi usado.
func (b *Bot) cmdDashboard(i *discordgo.InteractionCreate) {
	b.dashboard.moveTo(i.ChannelID)
	b.replyEphemeral(i, "✅ Painel fixado neste canal. Atualiza a cada "+b.cfg.RefreshInterval.String()+".")
}

// cmdContainerAction executa start/stop/restart no container informado.
func (b *Bot) cmdContainerAction(i *discordgo.InteractionCreate, action string) {
	name := optString(i, "container")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	timeout := int(b.cfg.ShutdownTimeout.Seconds())
	var err error
	var verb string
	switch action {
	case "start":
		err, verb = b.dx.Start(ctx, name), "iniciado"
	case "stop":
		err, verb = b.dx.Stop(ctx, name, timeout), "parado"
	case "restart":
		err, verb = b.dx.Restart(ctx, name, timeout), "reiniciado"
	}

	switch {
	case err == dockerx.ErrNotFound:
		b.replyEphemeral(i, "❌ Container `"+name+"` não encontrado.")
	case err != nil:
		b.replyEphemeral(i, "⚠️ Erro ao "+action+" `"+name+"`: "+err.Error())
	default:
		b.replyEphemeral(i, "✅ Container `"+name+"` "+verb+".")
	}
}

// handleAutocomplete devolve nomes de container que casam com o texto digitado.
func (b *Bot) handleAutocomplete(i *discordgo.InteractionCreate) {
	typed := strings.ToLower(optString(i, "container"))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	names, err := b.dx.Names(ctx)
	if err != nil {
		return
	}

	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, 25)
	for _, n := range names {
		if typed == "" || strings.Contains(strings.ToLower(n), typed) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: n, Value: n})
		}
		if len(choices) == 25 { // limite do Discord
			break
		}
	}

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	})
}

// optString extrai uma opção string da interação (comando ou autocomplete).
func optString(i *discordgo.InteractionCreate, name string) string {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

// replyEphemeral responde uma mensagem visível só para quem interagiu.
func (b *Bot) replyEphemeral(i *discordgo.InteractionCreate, msg string) {
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: msg,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
