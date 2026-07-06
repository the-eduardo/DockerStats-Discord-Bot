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

// hostEmbed monta o embed de um host. O host local usa métricas do gopsutil
// (CPU/RAM/disco/uptime); hosts remotos usam a Docker Info API (nº de CPUs e
// RAM total), pois não há acesso ao /proc deles. Um host inacessível vira
// um embed "offline".
func (b *Bot) hostEmbed(ctx context.Context, c *dockerx.Client) *discordgo.MessageEmbed {
	isLocal := c.Key == b.localHost().Key

	list, err := c.List(ctx)
	if err != nil {
		return &discordgo.MessageEmbed{
			Title:       "🔌 " + c.Label,
			Description: "Host inacessível no momento.",
			Color:       colorError,
			Timestamp:   time.Now().Format(time.RFC3339),
		}
	}
	c.CollectStats(ctx, list)

	var running int
	for _, ct := range list {
		if ct.State == "running" {
			running++
		}
	}

	var fields []*discordgo.MessageEmbedField
	color := colorOK
	footer := ""

	if isLocal {
		h := system.Collect(ctx, b.cfg.DiskPath)
		color = colorForCPU(h.CPUPercent)
		footer = "Uptime: " + humanDuration(h.Uptime)
		fields = []*discordgo.MessageEmbedField{
			{Name: "⚙️ CPU", Value: pct(h.CPUPercent), Inline: true},
			{Name: "🧠 RAM", Value: fmt.Sprintf("%s / %s", humanBytes(h.MemUsed), humanBytes(h.MemTotal)), Inline: true},
			{Name: "💾 Disco", Value: fmt.Sprintf("%s / %s", humanBytes(h.DiskUsed), humanBytes(h.DiskTotal)), Inline: true},
		}
	} else {
		ncpu, memTotal, ierr := c.Info(ctx)
		if ierr == nil {
			fields = []*discordgo.MessageEmbedField{
				{Name: "⚙️ CPUs", Value: fmt.Sprintf("%d", ncpu), Inline: true},
				{Name: "🧠 RAM total", Value: humanBytes(uint64(memTotal)), Inline: true},
			}
		}
		footer = "host remoto (via SSH)"
	}

	fields = append(fields, &discordgo.MessageEmbedField{
		Name:   fmt.Sprintf("📦 Containers (%d/%d rodando)", running, len(list)),
		Value:  renderContainers(list),
		Inline: false,
	})

	return &discordgo.MessageEmbed{
		Title:     "🖥️ " + c.Label,
		Color:     color,
		Fields:    fields,
		Footer:    &discordgo.MessageEmbedFooter{Text: footer},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// dashboardEmbeds monta um embed por host (na ordem: local primeiro).
func (b *Bot) dashboardEmbeds(ctx context.Context) []*discordgo.MessageEmbed {
	embeds := make([]*discordgo.MessageEmbed, 0, len(b.hosts))
	for _, c := range b.hosts {
		embeds = append(embeds, b.hostEmbed(ctx, c))
	}
	return embeds
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
	if len(out) > 1024 { // limite de um campo de embed
		out = out[:1000] + "\n… (lista truncada)"
	}
	return out
}

const (
	colorOK    = 0x2ecc71
	colorWarn  = 0xf1c40f
	colorBusy  = 0xe74c3c
	colorError = 0x992d22
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
