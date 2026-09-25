package discord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// confirmTimeout é a janela para o usuário confirmar uma ação destrutiva.
// var (não const) só para o teste poder encurtar a janela; nenhum outro ponto
// do código escreve nesta variável, e o valor de produção continua 30s.
var confirmTimeout = 30 * time.Second

type pendingConfirm struct {
	verb    string
	hostKey string
	name    string
	msgID   string
	cancel  context.CancelFunc
}

// confirmManager guarda confirmações pendentes e expira as não respondidas.
type confirmManager struct {
	bot *Bot
	mu  sync.Mutex
	m   map[string]*pendingConfirm
}

func newConfirmManager(b *Bot) *confirmManager {
	return &confirmManager{bot: b, m: make(map[string]*pendingConfirm)}
}

// add registra uma confirmação e agenda sua expiração. Se o tempo esgotar sem
// resposta, edita a mensagem (via token da interação `inter`) para "expirada".
func (cm *confirmManager) add(verb, hostKey, name string, inter *discordgo.Interaction) string {
	token := randToken()
	ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)

	msgID := ""
	if inter != nil && inter.Message != nil {
		msgID = inter.Message.ID
	}

	cm.mu.Lock()
	// Um clique duplo no mesmo botão cria DOIS tokens para a MESMA mensagem
	// efêmera (a mensagem passa a exibir só os botões do último). Sem esta
	// varredura, o primeiro token some do mapa só quando expira — e sua
	// goroutine de expiração edita a mensagem para "expirada" por cima do
	// resultado real de uma ação que o SEGUNDO token já executou. Cancelar
	// (sob o lock) é seguro: a goroutine alvo acorda com ctx.Err() ==
	// Canceled e retorna antes de tocar cm.mu ou editar a mensagem.
	if msgID != "" {
		for tok, p := range cm.m {
			if p.msgID == msgID {
				p.cancel()
				delete(cm.m, tok)
			}
		}
	}
	cm.m[token] = &pendingConfirm{verb: verb, hostKey: hostKey, name: name, msgID: msgID, cancel: cancel}
	cm.mu.Unlock()

	go func() {
		<-ctx.Done()
		if ctx.Err() != context.DeadlineExceeded {
			return // confirmado/cancelado antes do prazo
		}
		cm.mu.Lock()
		_, ok := cm.m[token]
		delete(cm.m, token)
		cm.mu.Unlock()
		if !ok {
			return
		}
		content := "⌛ Confirmação expirada."
		empty := []discordgo.MessageComponent{}
		_, _ = cm.bot.session.InteractionResponseEdit(inter, &discordgo.WebhookEdit{
			Content:    &content,
			Components: &empty,
		})
	}()

	return token
}

// pop retira e cancela (parando o timer) a confirmação identificada por token.
func (cm *confirmManager) pop(token string) (*pendingConfirm, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	p, ok := cm.m[token]
	if ok {
		p.cancel()
		delete(cm.m, token)
	}
	return p, ok
}

func randToken() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
