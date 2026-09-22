package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Este arquivo testa a FIAÇÃO do dedup de confirmação por mensagem: um clique
// duplo no mesmo botão (mesma mensagem efêmera) gera dois tokens, e sem
// invalidar o primeiro, a goroutine de expiração dele sobrescreve o resultado
// de uma ação que o SEGUNDO token já executou com "⌛ Confirmação expirada.".

// confirmClick monta uma interação de componente com Message preenchido —
// actionInteraction (action_refresh_wiring_test.go:100) não preenche esse
// campo de propósito (não precisa para os outros testes do pacote) e não deve
// ser alterado.
func confirmClick(customID, msgID string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent,
		ID:   "1", AppID: "2", Token: "tok",
		Message: &discordgo.Message{ID: msgID},
		Data:    discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

// tokenParaMsg devolve o token de UMA confirmação pendente para msgID. Em
// produção, o clique de confirmação sempre carrega o token da mensagem como
// ela está NA TELA — ou seja, o do último startConfirm. Aqui, no ambiente de
// teste (branco, mesmo pacote), lemos direto do mapa: com o fix, há só uma
// entrada por msgID (a sobrevivente); sem o fix, pode haver duas — e como o
// teste chama isto logo após o SEGUNDO clique, a entrada mais recente ainda é
// a única aceitável para simular "o botão que o usuário vê".
func tokenParaMsg(cm *confirmManager, msgID string) (string, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	for tok, p := range cm.m {
		if p.msgID == msgID {
			return tok, true
		}
	}
	return "", false
}

func TestConfirmOrfaoNaoSobrescreveResultado(t *testing.T) {
	old := confirmTimeout
	confirmTimeout = 80 * time.Millisecond
	defer func() { confirmTimeout = old }()

	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"

	// (a) clique duplo na MESMA mensagem efêmera: dois tokens nascem para
	// msg-1, o segundo deveria invalidar o primeiro.
	b.handleAction(confirmClick("act:stop:main:web", "msg-1"), "act:stop:main:web")
	b.handleAction(confirmClick("act:stop:main:web", "msg-1"), "act:stop:main:web")

	token, ok := tokenParaMsg(b.confirms, "msg-1")
	if !ok {
		t.Fatal("nenhuma confirmação pendente para msg-1 após os dois cliques")
	}

	// O usuário confirma (clica em ✅) — a ação REALMENTE roda.
	b.handleConfirm(confirmClick("cfm:ok:"+token, "msg-1"), "cfm:ok:"+token)

	// Espera passar da janela de expiração do PRIMEIRO token (que, sem o fix,
	// segue vivo no mapa e dispara sua própria edição "expirada" por cima do
	// resultado real que o handleConfirm acima acabou de escrever).
	time.Sleep(400 * time.Millisecond)

	for _, body := range rt.bodiesCopy() {
		if strings.Contains(string(body), "Confirmação expirada") {
			t.Fatal("clique duplo: confirmação órfã sobrescreveu o resultado real com \"expirada\"")
		}
	}
}

// Contraprova obrigatória: sem ela, um fix que matasse a expiração inteira
// (em vez de só deduplicar) também faria o teste acima passar.
func TestConfirmUnicoAindaExpira(t *testing.T) {
	old := confirmTimeout
	confirmTimeout = 80 * time.Millisecond
	defer func() { confirmTimeout = old }()

	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"

	b.handleAction(confirmClick("act:stop:main:web", "msg-2"), "act:stop:main:web")

	time.Sleep(400 * time.Millisecond)

	found := false
	for _, body := range rt.bodiesCopy() {
		if strings.Contains(string(body), "Confirmação expirada") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("contraprova falhou: confirmação única deveria expirar normalmente")
	}
}
