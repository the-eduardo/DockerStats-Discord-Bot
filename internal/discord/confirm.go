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
const confirmTimeout = 30 * time.Second

type pendingConfirm struct {
	verb   string
	name   string
	cancel context.CancelFunc
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
func (cm *confirmManager) add(verb, name string, inter *discordgo.Interaction) string {
	token := randToken()
	ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)

	cm.mu.Lock()
	cm.m[token] = &pendingConfirm{verb: verb, name: name, cancel: cancel}
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
