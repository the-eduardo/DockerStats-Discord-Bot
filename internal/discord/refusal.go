package discord

import (
	"fmt"
	"sync"
	"time"
)

// refusalWindow é a janela de agregação das recusas por rate limit: o mesmo
// tempo que o balde do rateLimiter (newRateLimiter(8, 0.5), bot.go:70) leva
// para reencher do zero (max/refill = 8/0.5 = 16s). Fechar a janela quando o
// balde reenche alinha o embed agregado com o momento em que a recusa parou
// de fazer sentido.
const refusalWindow = 16 * time.Second

// refusalAudit agrega recusas por rate limit numa janela e publica UM embed
// por janela, em vez de um por recusa. Auditar 1-para-1 amplificava
// exatamente a rajada que o limiter está contendo (30 cliques recusados
// viravam 30 embeds) — achado do painel Dev Senior na drenagem de 26/08/2026,
// que bloqueou a versão anterior desta mudança.
type refusalAudit struct {
	mu    sync.Mutex
	n     int
	open  bool
	first auditEntry
	after func(time.Duration) <-chan time.Time // seam de teste; nil = time.After
}

// auditRefusal registra uma recusa por rate limit. A primeira de cada janela
// abre a janela e sobe a goroutine que a fecha; as demais só incrementam o
// contador — no máximo uma goroutine de espera viva por vez.
func (b *Bot) auditRefusal(e auditEntry) {
	ra := &b.refusals
	ra.mu.Lock()
	ra.n++
	if ra.open {
		ra.mu.Unlock()
		return
	}
	ra.open = true
	ra.first = e
	after := ra.after
	ra.mu.Unlock()

	if after == nil {
		after = time.After
	}
	go func() {
		<-after(refusalWindow)
		b.flushRefusals()
	}()
}

// flushRefusals fecha a janela aberta (se houver) e publica o agregado.
// Idempotente com n == 0: chamado tanto pelo timer da janela quanto pelo
// Stop() (bot.go), a corrida entre os dois nunca produz mais de 1 embed.
func (b *Bot) flushRefusals() {
	ra := &b.refusals
	ra.mu.Lock()
	n := ra.n
	e := ra.first
	ra.n = 0
	ra.open = false
	ra.mu.Unlock()

	if n == 0 {
		return
	}
	// detail e' texto livre por acao individual (ex.: o comando do /exec); com
	// N acoes agregadas ele deixa de fazer sentido como campo unico.
	e.detail = ""
	e.action = "rate-limit"
	e.result = fmt.Sprintf("⏳ %d ação(ões) recusada(s) por rate limit em %s", n, refusalWindow)
	b.audit(e)
}
