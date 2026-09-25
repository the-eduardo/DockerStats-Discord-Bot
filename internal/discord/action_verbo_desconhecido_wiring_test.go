package discord

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Este arquivo testa a FIAÇÃO do fail-closed do ramo default de handleAction:
// um verbo desconhecido no custom_id (custom_id forjado ou mensagem-painel de
// versão antiga) não deve ser auditado, não deve gastar token do rate limiter
// e não deve ecoar o texto do cliente em lugar nenhum. Antes disto, "default"
// cobria start/pause/unpause E qualquer verbo desconhecido — e o verbo
// desconhecido seguia até runActionAudited, que grava e.action CRU (sem teto
// nem escape) no campo Ação da auditoria.

// corpoTemEmbed decodifica um corpo HTTP e diz se ele carrega pelo menos um
// embed — só o audit() (via ChannelMessageSendEmbed) produz embed neste
// fluxo; handleAction responde sempre por Content puro (InteractionRespond/
// InteractionResponseEdit). Mais robusto que strings.Contains sobre o dump
// bruto: um embed genérico não se confunde com o Content da resposta ao
// usuário.
func corpoTemEmbed(body []byte) bool {
	var payload struct {
		Embeds []json.RawMessage `json:"embeds"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	return len(payload.Embeds) > 0
}

func esperaSemEmbed(t *testing.T, rt *dashboardPostCountingTransport) {
	t.Helper()
	deadline := time.After(500 * time.Millisecond)
	for {
		for _, b := range rt.bodiesCopy() {
			if corpoTemEmbed(b) {
				t.Fatal("verbo desconhecido foi auditado: apareceu um embed onde não devia haver nenhum")
			}
		}
		select {
		case <-deadline:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func esperaComEmbed(t *testing.T, rt *dashboardPostCountingTransport) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		for _, b := range rt.bodiesCopy() {
			if corpoTemEmbed(b) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatal("controle positivo falhou: act:start:main:web deveria ter produzido um embed de auditoria")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestHandleActionVerboDesconhecidoNaoAuditaNaoGastaTokenNaoEcoa(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999" // helper monta cfg vazio, que desliga a auditoria

	b.handleAction(actionInteraction("act:sabotagem:main:web"), "act:sabotagem:main:web")

	// A) nenhum embed de auditoria foi produzido para o verbo desconhecido.
	esperaSemEmbed(t, rt)

	// B) o verbo desconhecido não gastou nenhum token do rate limiter: as 8
	// permissões do bucket cheio continuam disponíveis.
	for n := 1; n <= 8; n++ {
		if !b.limiter.Allow() {
			t.Fatalf("limiter recusou a permissão %d/8 — o verbo desconhecido consumiu token indevidamente", n)
		}
	}

	// C) o verbo do cliente não foi ecoado em nenhum corpo enviado ao Discord
	// (nem no Content da resposta, nem em campo de auditoria).
	for _, b := range rt.bodiesCopy() {
		if strings.Contains(string(b), "sabotagem") {
			t.Fatal("o verbo desconhecido foi ecoado num corpo enviado ao Discord")
		}
	}

	// Controle positivo: sem isto, A e B passariam também com o bot quebrado
	// (ex.: auditoria inteira desligada por engano faria qualquer verbo
	// "passar" nesta bateria).
	b2, rt2 := newActionWiringBot(t)
	b2.cfg.AuditChannelID = "999"
	b2.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	esperaComEmbed(t, rt2)
}

// TestHandleActionVerbosDiretosContinuamAuditando fecha o outro lado da troca
// default→case explícito: o controle positivo acima só exercita "start", e
// tirar "pause" ou "unpause" da lista passava verde — o botão cairia em
// "Ação desconhecida." sem executar. Escrito na drenagem de 25/09/2026.
func TestHandleActionVerbosDiretosContinuamAuditando(t *testing.T) {
	for _, verb := range []string{"start", "pause", "unpause"} {
		t.Run(verb, func(t *testing.T) {
			b, rt := newActionWiringBot(t)
			b.cfg.AuditChannelID = "999"
			cid := "act:" + verb + ":main:web"
			b.handleAction(actionInteraction(cid), cid)
			esperaComEmbed(t, rt)
			for _, body := range rt.bodiesCopy() {
				if strings.Contains(string(body), "Ação desconhecida") {
					t.Fatalf("verbo %q caiu no ramo de verbo desconhecido", verb)
				}
			}
		})
	}
}
