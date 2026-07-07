package discord

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// auditEntry descreve uma ação a ser registrada no canal de auditoria.
type auditEntry struct {
	actor   string // quem executou (username)
	action  string // start, stop, exec, logs...
	host    string // rótulo do host
	target  string // nome do container
	detail  string // extra (ex.: comando do exec)
	result  string // mensagem de resultado (começa com ✅/❌/⚠️)
}

// audit publica (best-effort, assíncrono) um registro no canal de auditoria.
// Não faz nada se AUDIT_CHANNEL_ID não estiver configurado.
func (b *Bot) audit(e auditEntry) {
	if b.cfg.AuditChannelID == "" {
		return
	}

	color := colorOK
	switch {
	case strings.HasPrefix(e.result, "❌"), strings.HasPrefix(e.result, "⚠️"):
		color = colorBusy
	}

	fields := []*discordgo.MessageEmbedField{
		{Name: "Ação", Value: "`" + e.action + "`", Inline: true},
		{Name: "Host", Value: nonEmpty(e.host), Inline: true},
		{Name: "Container", Value: "`" + nonEmpty(e.target) + "`", Inline: true},
	}
	if e.detail != "" {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Detalhe", Value: truncate(e.detail, 1024), Inline: false,
		})
	}
	if e.result != "" {
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Resultado", Value: truncate(e.result, 1024), Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Author:    &discordgo.MessageEmbedAuthor{Name: nonEmpty(e.actor)},
		Color:     color,
		Fields:    fields,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Assíncrono: auditoria nunca deve atrasar/derrubar a ação principal.
	go func() {
		_, _ = b.session.ChannelMessageSendEmbed(b.cfg.AuditChannelID, embed)
	}()
}

// actorName extrai o nome de quem disparou a interação.
func actorName(i *discordgo.InteractionCreate) string {
	switch {
	case i.Member != nil && i.Member.User != nil:
		return i.Member.User.Username
	case i.User != nil:
		return i.User.Username
	}
	return "desconhecido"
}

func nonEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
