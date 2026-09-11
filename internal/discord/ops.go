package discord

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// maxBlock deixa margem sob o limite de 2000 caracteres de uma mensagem.
const maxBlock = 1850

// maxAttach deixa margem sob o teto de upload de bot/webhook (~8 MiB desde
// jan/2025). Sem isso, /logs com janela grande estoura o limite e a
// interação fica presa em "thinking..." (medido: 24h de dsbot-socket-proxy
// já passam de 8.5 MiB).
const maxAttach = 7 << 20

// tailBytes mantém só os últimos max bytes de s, avançando o corte até a
// próxima quebra de linha para nunca partir uma linha (ou rune multi-byte)
// ao meio.
func tailBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := len(s) - max
	if idx := strings.IndexByte(s[cut:], '\n'); idx >= 0 {
		cut += idx + 1
	} else {
		// Log de uma linha só (JSON despejado, barra de progresso com \r): sem
		// \n à frente para alinhar o corte, ele pode cair no meio de um rune
		// multi-byte e o anexo sai com UTF-8 inválido. Avança até o próximo
		// início de rune. Medido antes do fix: 60 de 350 posições de corte
		// produziam saída inválida em log acentuado sem quebra de linha.
		for cut < len(s) && !utf8.RuneStart(s[cut]) {
			cut++
		}
	}
	return "…(truncado: mostrando os últimos bytes do log)\n" + s[cut:]
}

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
	hostKey, name := parseTarget(optString(i, "container"))
	host := b.hostByKey(hostKey)
	mins := int(optInt(i, "minutes"))
	if mins <= 0 || mins > 1440 {
		mins = 30
	}

	// README promete "every action is logged" (who, what, host, container,
	// result) — ler o log de um container pode expor segredo que outro
	// processo gravou em stderr, e este era o único handler da superfície
	// perigosa sem rastro no canal de auditoria. defer garante que TODO
	// caminho de saída (host desconhecido, erro, sucesso) audita.
	hostLabel := hostKey
	if host != nil {
		hostLabel = host.Label
	}
	janela := strconv.Itoa(mins) + " min"
	result := "❌ Host desconhecido."
	defer func() {
		b.audit(auditEntry{actor: actorName(i), action: "logs", host: hostLabel, target: name, detail: janela, result: result})
	}()

	// Sem flag efêmera: assim o anexo de arquivo (para logs grandes) funciona.
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if host == nil {
		b.editResponse(i, "❌ Host desconhecido.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	out, err := host.Logs(ctx, name, time.Duration(mins)*time.Minute)
	if err != nil {
		result = "⚠️ erro: " + err.Error()
		b.editResponse(i, "⚠️ Erro ao ler logs de `"+name+"`: "+err.Error())
		return
	}
	if strings.TrimSpace(out) == "" {
		out = "(sem saída)"
	}

	header := "📜 **" + name + "** — últimos " + strconv.Itoa(mins) + " min"
	if len(out) <= maxBlock {
		if err := b.editResponse(i, header+":\n"+codeBlock(out)); err != nil {
			log.Printf("/logs %s: %v", name, err)
			result = "⚠️ publicação falhou: " + err.Error()
			return
		}
		result = "✅ publicado no canal (inline)"
		return
	}

	out = tailBytes(out, maxAttach)
	content := header + " (anexo):"
	if _, err := b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Files: []*discordgo.File{{
			Name:        name + ".log",
			ContentType: "text/plain",
			Reader:      strings.NewReader(out),
		}},
	}); err != nil {
		log.Printf("anexo /logs %s: %v", name, err)
		result = "⚠️ anexo falhou: " + err.Error()
		b.editResponse(i, header+" — falha ao enviar o anexo, tente uma janela menor de minutos.")
		return
	}
	result = "✅ publicado no canal (anexo .log)"
}

// showLogsEphemeral atende o botão "Logs" do painel: espiada rápida (efêmera)
// dos últimos 30 min.
func (b *Bot) showLogsEphemeral(i *discordgo.InteractionCreate, hostKey, name string) {
	host := b.hostByKey(hostKey)
	hostLabel := hostKey
	if host != nil {
		hostLabel = host.Label
	}
	// Mesmo motivo do cmdLogs: leitura efêmera também é leitura, e a distinção
	// "efêmero" vs "publicado no canal" no Resultado é o que diz ao revisor se
	// o conteúdo saiu do escopo privado do clique.
	result := "❌ Host desconhecido."
	defer func() {
		b.audit(auditEntry{actor: actorName(i), action: "logs", host: hostLabel, target: name, detail: "30 min", result: result})
	}()

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
	if host == nil {
		b.editResponse(i, "❌ Host desconhecido.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := host.Logs(ctx, name, 30*time.Minute)
	if err != nil {
		result = "⚠️ erro: " + err.Error()
		b.editResponse(i, "⚠️ Erro ao ler logs de `"+name+"`: "+err.Error())
		return
	}
	if err := b.editResponse(i, "📜 **"+name+"** (últimos 30 min):\n"+codeBlock(out)); err != nil {
		log.Printf("botão logs %s: %v", name, err)
		result = "⚠️ publicação falhou: " + err.Error()
		return
	}
	result = "✅ efêmero (inline)"
}

// ---- /exec (modal) ----

// cmdExec abre um modal para o usuário digitar o comando a executar.
func (b *Bot) cmdExec(i *discordgo.InteractionCreate) {
	hostKey, name := parseTarget(optString(i, "container"))
	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: "exec:" + target(hostKey, name),
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
	hostKey, name := parseTarget(strings.TrimPrefix(data.CustomID, "exec:"))
	host := b.hostByKey(hostKey)
	cmd := strings.TrimSpace(modalValue(data, "cmd"))
	if cmd == "" {
		b.replyEphemeral(i, "Comando vazio.")
		return
	}
	if host == nil {
		b.replyEphemeral(i, "❌ Host desconhecido.")
		return
	}

	// Allow-list opcional: se configurada, restringe o exec (Fase 5).
	if reason, ok := b.execAllowed(cmd); !ok {
		b.replyEphemeral(i, "⛔ Comando bloqueado: "+reason)
		b.audit(auditEntry{actor: actorName(i), action: "exec", host: host.Label, target: name, detail: cmd, result: "⛔ bloqueado: " + reason})
		return
	}
	if !b.limiter.Allow() {
		b.replyEphemeral(i, "⏳ Muitas ações em pouco tempo — aguarde alguns segundos.")
		b.auditRefusal(auditEntry{actor: actorName(i), action: "exec", host: host.Label, target: name})
		return
	}

	_ = b.session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, exitCode, err := host.Exec(ctx, name, cmd)

	var result string
	switch {
	case err != nil:
		result = "⚠️ erro: " + err.Error()
		b.editResponse(i, "⚠️ Erro no exec em `"+name+"`: "+err.Error())
	case exitCode > 0:
		result = fmt.Sprintf("❌ exit code %d", exitCode)
		b.editResponse(i, "`$ "+truncate(cmd, 120)+"` em **"+name+"**:\n"+codeBlock(out))
	case exitCode < 0:
		result = "⚠️ executado, exit code não confirmado"
		b.editResponse(i, "`$ "+truncate(cmd, 120)+"` em **"+name+"**:\n"+codeBlock(out))
	default:
		result = "✅ executado"
		b.editResponse(i, "`$ "+truncate(cmd, 120)+"` em **"+name+"**:\n"+codeBlock(out))
	}
	b.audit(auditEntry{actor: actorName(i), action: "exec", host: host.Label, target: name, detail: cmd, result: result})
}

// execAllowed aplica a EXEC_ALLOWLIST. Vazia = tudo permitido. Quando ativa,
// bloqueia encadeamento/metacaracteres de shell e exige que o primeiro token do
// comando esteja na lista. É um guardrail (não um sandbox).
func (b *Bot) execAllowed(cmd string) (string, bool) {
	if len(b.cfg.ExecAllowlist) == 0 {
		return "", true
	}
	if strings.ContainsAny(cmd, ";&|`$><\n") {
		return "encadeamento/metacaracteres não são permitidos com a allow-list ativa", false
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return "comando vazio", false
	}
	for _, allowed := range b.cfg.ExecAllowlist {
		if fields[0] == allowed {
			return "", true
		}
	}
	return "`" + fields[0] + "` não está na allow-list", false
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
func (b *Bot) editResponse(i *discordgo.InteractionCreate, content string) error {
	_, err := b.session.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Content: &content})
	return err
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
