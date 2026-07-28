package discord

import (
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Dead-man switch do Uptime Kuma (item 5 da auditoria de observabilidade).
//
// O monitor docker "Docker-StatusBot" fica UP mesmo com o bot incapaz de falar
// com o Discord (token revogado, rate limit, gateway em backoff). O push abaixo
// so acontece DEPOIS de um render bem-sucedido do painel -- ou seja, so quando
// o Discord aceitou de fato uma chamada nossa.
//
// Sem KUMA_PUSH_URL no ambiente e no-op: nada muda para quem nao configurou.

var (
	kumaClient   = &http.Client{Timeout: 10 * time.Second}
	kumaWarnOnce sync.Once
)

func pushKuma() {
	url := os.Getenv("KUMA_PUSH_URL")
	if url == "" {
		return
	}
	resp, err := kumaClient.Get(url + "?status=up&msg=dashboard+ok")
	if err != nil {
		// Loga uma unica vez: o painel nao pode virar spam de log se o Kuma cair.
		kumaWarnOnce.Do(func() { log.Printf("push do Kuma falhou: %v", err) })
		return
	}
	_ = resp.Body.Close()
}
