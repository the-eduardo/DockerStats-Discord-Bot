package discord

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/system"
)

// buildDashboardEmbed monta o embed que resume host + containers. É a peça
// central reutilizada tanto pelo /status (Fase 1) quanto pelo painel
// persistente auto-atualizável (Fase 2).
func (b *Bot) buildDashboardEmbed(ctx context.Context) *discordgo.MessageEmbed {
	host := system.Collect(ctx, b.cfg.DiskPath)

	list, err := b.dx.List(ctx)
	if err != nil {
		return &discordgo.MessageEmbed{
			Title:       "🖥️ " + b.cfg.Hostname,
			Description: "⚠️ Erro ao listar containers: " + err.Error(),
			Color:       colorError,
			Timestamp:   time.Now().Format(time.RFC3339),
		}
	}
	b.dx.CollectStats(ctx, list)

	var running int
	for _, c := range list {
		if c.State == "running" {
			running++
		}
	}

	fields := []*discordgo.MessageEmbedField{
		{
			Name: "⚙️ CPU",
			Value: fmt.Sprintf("%s", pct(host.CPUPercent)),
			Inline: true,
		},
		{
			Name:   "🧠 RAM",
			Value:  fmt.Sprintf("%s / %s", humanBytes(host.MemUsed), humanBytes(host.MemTotal)),
			Inline: true,
		},
		{
			Name:   "💾 Disco",
			Value:  fmt.Sprintf("%s / %s", humanBytes(host.DiskUsed), humanBytes(host.DiskTotal)),
			Inline: true,
		},
	}

	// Bloco de containers em code block para alinhar no celular.
	fields = append(fields, &discordgo.MessageEmbedField{
		Name:   fmt.Sprintf("📦 Containers (%d/%d rodando)", running, len(list)),
		Value:  renderContainers(list),
		Inline: false,
	})

	return &discordgo.MessageEmbed{
		Title:     "🖥️ " + b.cfg.Hostname,
		Color:     colorForCPU(host.CPUPercent),
		Fields:    fields,
		Footer:    &discordgo.MessageEmbedFooter{Text: "Uptime: " + humanDuration(host.Uptime) + " · atualizado"},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// renderContainers monta a lista textual de containers com estado, CPU e RAM.
func renderContainers(list []dockerx.Container) string {
	if len(list) == 0 {
		return "_nenhum container encontrado_"
	}

	var sb strings.Builder
	for _, c := range list {
		sb.WriteString(stateEmoji(c.State))
		sb.WriteString(" **")
		sb.WriteString(c.Name)
		sb.WriteString("**\n")
		if c.State == "running" {
			sb.WriteString(fmt.Sprintf("` CPU %5s · RAM %s `\n", pct(c.CPUPercent), humanBytes(c.MemUsage)))
		} else {
			sb.WriteString("` " + c.Status + " `\n")
		}
	}

	out := sb.String()
	// Campo de embed do Discord tem limite de 1024 caracteres.
	if len(out) > 1024 {
		out = out[:1000] + "\n… (lista truncada)"
	}
	return out
}

const (
	colorOK    = 0x2ecc71 // verde
	colorWarn  = 0xf1c40f // amarelo
	colorBusy  = 0xe74c3c // vermelho
	colorError = 0x992d22 // vermelho escuro
)

// colorForCPU escolhe a cor do embed conforme a carga de CPU do host.
func colorForCPU(cpu float64) int {
	switch {
	case cpu >= 85:
		return colorBusy
	case cpu >= 60:
		return colorWarn
	default:
		return colorOK
	}
}
