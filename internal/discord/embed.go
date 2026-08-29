package discord

import (
	"context"
	"fmt"
	"log"
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
	embed, _, _ := b.hostEmbedWithList(ctx, c)
	return embed
}

// hostEmbedWithList é o corpo de hostEmbed, mas também devolve a lista de
// containers já coletada (e o erro de c.List) — permite que dashboardCollect
// reaproveite a MESMA coleta para montar o select menu, em vez de listar os
// containers de novo (ver dashboardCollect e components.go).
func (b *Bot) hostEmbedWithList(ctx context.Context, c *dockerx.Client) (*discordgo.MessageEmbed, []dockerx.Container, error) {
	isLocal := c.Key == b.localHost().Key

	list, err := c.List(ctx)
	if err != nil {
		log.Printf("hostEmbed %q: %v", c.Key, err)
		return &discordgo.MessageEmbed{
			Title:       "🔌 " + c.Label,
			Description: "Host inacessível no momento.",
			Color:       colorError,
			Timestamp:   time.Now().Format(time.RFC3339),
		}, nil, err
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
		} else {
			log.Printf("hostEmbed %q: Info: %v", c.Key, ierr)
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
	}, list, nil
}

// dashboardEmbeds monta um embed por host (na ordem: local primeiro).
func (b *Bot) dashboardEmbeds(ctx context.Context) []*discordgo.MessageEmbed {
	embeds, _ := b.dashboardCollect(ctx)
	return embeds
}

// dashboardCollect monta um embed por host E devolve, para os hosts que
// responderam com sucesso, a lista de containers já coletada — usada por
// componentsFrom para montar o select menu sem listar os containers de novo
// (antes render() chamava List() duas vezes por host a cada ciclo: uma via
// hostEmbed, outra via buildDashboardComponents).
func (b *Bot) dashboardCollect(ctx context.Context) ([]*discordgo.MessageEmbed, []hostContainers) {
	embeds := make([]*discordgo.MessageEmbed, 0, len(b.hosts))
	hosts := make([]hostContainers, 0, len(b.hosts))
	for _, c := range b.hosts {
		embed, list, err := b.hostEmbedWithList(ctx, c)
		embeds = append(embeds, embed)
		if err == nil {
			hosts = append(hosts, hostContainers{key: c.Key, label: c.Label, containers: list})
		}
	}
	return embeds, hosts
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
	// Corta por RUNE, nao por byte: os emojis de estado (stateEmoji, format.go)
	// sao multibyte, e um slice de byte pode parti-los ao meio — o painel mostra
	// "\ufffd" no lugar. Com 21 containers neste host a lista ja passa dos 1024
	// chars, entao o caminho e' exercitado de verdade. truncate() (format.go) ja
	// faz isso certo e e' usado com o mesmo limite em audit.go.
	if len([]rune(out)) > 1024 { // limite de um campo de embed
		out = truncate(out, 1000) + "\n… (lista truncada)"
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
