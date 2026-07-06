package discord

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// maxBlock deixa margem sob o limite de 2000 caracteres de uma mensagem.
const maxBlock = 1850

// codeBlock envolve a saída num bloco de código, mantendo o FINAL quando excede
// (as últimas linhas costumam ser as mais relevantes em logs/exec).
func codeBlock(s string) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		s = "(sem saída)"
	}
	if len(s) > maxBlock {
		s = "…(truncado)\n" + s[len(s)-maxBlock:]
	}
	return "```\n" + s + "\n```"
}

// ---- /logs ----

// cmdLogs busca os logs e responde efêmero; se a saída for grande, anexa .log.
func (b *Bot) cmdLogs(i *discordgo.InteractionCreate) {
	name := optString(i, "container")
	mins := int(optInt(i, "minutes"))
	if mins <= 0 || mins > 1440 {
		mins = 30
	}

	// Sem flag efêmera: assim o anexo de arquivo (para logs grandes) funciona.
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	out, err := b.dx.Logs(ctx, name, time.Duration(mins)*time.Minute)
	if err != nil {
		b.editResponse(i, "⚠️ Erro ao ler logs de `"+name+"`: "+err.Error())
		return
	}
	if strings.TrimSpace(out) == "" {
		out = "(sem saída)"
	}

	header := "📜 **" + name + "** — últimos " + strconv.Itoa(mins) + " min"
	if len(out) <= maxBlock {
		b.editResponse(i, header+":\n"+codeBlock(out))
		return
	}

	content := header + " (anexo):"
	_, _ = b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Files: []*discordgo.File{{
			Name:        name + ".log",
			ContentType: "text/plain",
			Reader:      strings.NewReader(out),
		}},
	})
}

// showLogsEphemeral atende o botão "Logs" do painel: espiada rápida (efêmera)
// das últimas 50 linhas.
func (b *Bot) showLogsEphemeral(i *discordgo.InteractionCreate, name string) {
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := b.dx.Logs(ctx, name, 30*time.Minute)
	if err != nil {
		b.editResponse(i, "⚠️ Erro ao ler logs de `"+name+"`: "+err.Error())
		return
	}
	b.editResponse(i, "📜 **"+name+"** (últimos 30 min):\n"+codeBlock(out))
}

// ---- /exec (modal) ----

// cmdExec abre um modal para o usuário digitar o comando a executar.
func (b *Bot) cmdExec(i *discordgo.InteractionCreate) {
	name := optString(i, "container")
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "exec:" + name,
			Title:    truncate("Exec: "+name, 45),
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{
						CustomID:    "cmd",
						Label:       "Comando (executado via sh -c)",
						Style:       discordgo.TextInputParagraph,
						Placeholder: "ls -la /",
						Required:    true,
						MaxLength:   400,
					},
				}},
			},
		},
	})
}

// handleModal processa a submissão do modal de exec.
func (b *Bot) handleModal(i *discordgo.InteractionCreate) {
	data := i.ModalSubmitData()
	if !strings.HasPrefix(data.CustomID, "exec:") {
		return
	}
	name := strings.TrimPrefix(data.CustomID, "exec:")
	cmd := modalValue(data, "cmd")
	if strings.TrimSpace(cmd) == "" {
		b.replyEphemeral(i, "Comando vazio.")
		return
	}

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := b.dx.Exec(ctx, name, cmd)
	if err != nil {
		b.editResponse(i, "⚠️ Erro no exec em `"+name+"`: "+err.Error())
		return
	}
	b.editResponse(i, "`$ "+truncate(cmd, 120)+"` em **"+name+"**:\n"+codeBlock(out))
}

// modalValue extrai o valor de um TextInput da submissão do modal.
func modalValue(data discordgo.ModalSubmitInteractionData, id string) string {
	for _, row := range data.Components {
		ar, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, comp := range ar.Components {
			if ti, ok := comp.(*discordgo.TextInput); ok && ti.CustomID == id {
				return ti.Value
			}
		}
	}
	return ""
}

// editResponse edita a resposta (deferred) da interação com um texto.
func (b *Bot) editResponse(i *discordgo.InteractionCreate, content string) {
	_, _ = b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
}

// optInt lê uma opção inteira da interação de comando.
func optInt(i *discordgo.InteractionCreate, name string) int64 {
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == name {
			return opt.IntValue()
		}
	}
	return 0
}
