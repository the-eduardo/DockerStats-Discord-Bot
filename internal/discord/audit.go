package discord

import (
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// auditEntry descreve uma ação a ser registrada no canal de auditoria.
type auditEntry struct {
	actor  string // quem executou (username)
	action string // start, stop, exec, logs...
	host   string // rótulo do host
	target string // nome do container
	detail string // extra (ex.: comando do exec)
	result string // mensagem de resultado (começa com ✅/❌/⚠️)
}

// audit publica (best-effort, assíncrono) um registro no canal de auditoria.
// Não faz nada se AUDIT_CHANNEL_ID não estiver configurado.
func (b *Bot) audit(e auditEntry) {
	if b.cfg.AuditChannelID == "" {
		return
	}

	// Verde SO no sucesso explicito. A regra anterior era a inversa (lista de
	// prefixos "ruins") e todo prefixo novo nascia VERDE por omissao: ja custou
	// o ⏳ da recusa agregada (29/08) e custava o ⛔ do exec barrado pela
	// allow-list (ops.go:182), que saia com a cor de uma acao bem-sucedida.
	color := colorBusy
	if e.result == "" || strings.HasPrefix(e.result, "✅") {
		color = colorOK
	}

	// e.target e' texto LIVRE (nome de container vindo de opcao com
	// Autocomplete: true, que nao restringe o valor enviado — commands.go:18-24).
	// Sem teto, um nome com mais de ~1022 runes estoura o limite de 1024 do
	// campo do embed, o Discord rejeita o embed inteiro (400) e o registro
	// daquela acao se perde por completo, em silencio (o erro e' descartado
	// logo abaixo). Sem o ReplaceAll, um backtick no nome fecha a cerca cedo e
	// o resto do campo volta a ser markdown — mesmo vetor ja fechado pro
	// Detalhe na drenagem de 25/08/2026. 250 runes cabem folgado em qualquer
	// nome real de container.
	alvo := strings.ReplaceAll(truncate(nonEmpty(e.target), 250), "`", "'")
	fields := []*discordgo.MessageEmbedField{
		{Name: "Ação", Value: "`" + e.action + "`", Inline: true},
		{Name: "Host", Value: nonEmpty(e.host), Inline: true},
		{Name: "Container", Value: "`" + alvo + "`", Inline: true},
	}
	if e.detail != "" {
		// e.detail e' texto LIVRE do usuario (o comando do /exec, ops.go:181 e
		// :205). Sem cerca, quem tem permissao de exec — mas foi barrado pela
		// allow-list ou pelo rate limit — consegue gravar markdown no canal de
		// AUDITORIA: link mascarado ([abrir log](http://phishing)), ||spoiler||
		// escondendo o comando real, ou uma cerca de backtick que quebra a
		// formatacao do embed. O registro que deveria ser prova de incidente
		// vira superficie de manipulacao. Mencao (@everyone) nao e' risco aqui:
		// o Discord so faz parsing dela em .Content, nunca em embed.
		// Achado do painel AppSec na drenagem de 25/08/2026.
		//
		// A cerca resolve tudo de uma vez (o conteudo vira literal); o
		// ReplaceAll do backtick impede que o proprio texto feche a cerca.
		// 1000 e nao 1024: os delimitadores da cerca custam 8 caracteres.
		safe := strings.ReplaceAll(truncate(e.detail, 1000), "`", "'")
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Detalhe", Value: "```\n" + safe + "\n```", Inline: false,
		})
	}
	if e.result != "" {
		// e.result interpola o mesmo nome de texto livre (runAction,
		// components.go) dentro de spans de backtick — sem cerca aqui, um
		// nome com backtick ou markdown de link vira link mascarado no canal
		// de auditoria. Mesmo tratamento do Detalhe: cerca + ReplaceAll.
		safe := strings.ReplaceAll(truncate(e.result, 1000), "`", "'")
		fields = append(fields, &discordgo.MessageEmbedField{
			Name: "Resultado", Value: "```\n" + safe + "\n```", Inline: false,
		})
	}

	embed := &discordgo.MessageEmbed{
		Author:    &discordgo.MessageEmbedAuthor{Name: nonEmpty(e.actor)},
		Color:     color,
		Fields:    fields,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// Assíncrono: auditoria nunca deve atrasar/derrubar a ação principal.
	b.auditWG.Add(1)
	go func() {
		defer b.auditWG.Done()
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
