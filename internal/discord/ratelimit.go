package discord

import (
	"sync"
	"time"
)

// rateLimiter é um token bucket simples. Serve para conter rajadas acidentais
// de ações mutáveis (ex.: toques repetidos no celular), não abuso — o bot já é
// de dono único.
type rateLimiter struct {
	mu     sync.Mutex
	tokens float64
	max    float64
	refill float64 // tokens por segundo
	last   time.Time
}

func newRateLimiter(max, refillPerSec float64) *rateLimiter {
	return &rateLimiter{tokens: max, max: max, refill: refillPerSec, last: time.Now()}
}

// Allow consome um token se houver; caso contrário devolve false.
func (r *rateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.tokens += now.Sub(r.last).Seconds() * r.refill
	if r.tokens > r.max {
		r.tokens = r.max
	}
	r.last = now

	if r.tokens >= 1 {
		r.tokens--
		return true
	}
	return false
}
