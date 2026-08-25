package discord

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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
	kumaClient = &http.Client{Timeout: 10 * time.Second}

	kumaMu      sync.Mutex
	kumaFailing bool
)

func pushKuma() {
	pushURL := os.Getenv("KUMA_PUSH_URL")
	if pushURL == "" {
		return
	}
	resp, err := kumaClient.Get(pushURL + "?status=up&msg=dashboard+ok")
	if err != nil {
		// err.Error() de *url.Error carrega a URL COMPLETA, e KUMA_PUSH_URL
		// embute o push token no path (/api/push/<token>). Como kumaState agora
		// loga a cada TRANSICAO (e nao mais uma vez por processo), logar o erro
		// cru publicaria o token no Loki a cada flap do Kuma. Desembrulhar
		// preserva a causa (dial/timeout/DNS) e descarta a URL.
		detalhe := "erro de rede"
		var uerr *url.Error
		if errors.As(err, &uerr) {
			detalhe = uerr.Err.Error()
		}
		kumaState(false, detalhe)
		return
	}
	// Drena antes de fechar para o transport reaproveitar a conexao em vez de
	// descartar e abrir uma nova TCP a cada ciclo.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()

	// O Kuma responde 404 (nao so erro de transporte) quando o push token e
	// invalido OU o monitor esta pausado/removido -- silencio ali e' o mesmo
	// tipo de cegueira que o dead-man switch existe para evitar.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		kumaState(false, fmt.Sprintf("status %s", resp.Status))
		return
	}
	kumaState(true, "")
}

// kumaState loga so na TRANSICAO (ok->falha ou falha->ok). Evita duas
// armadilhas: log incondicional vira spam a cada ciclo se o Kuma ficar
// fora por muito tempo, e sync.Once (o design anterior) loga a falha uma
// unica vez na vida do processo e fica cego para sempre depois dela.
func kumaState(ok bool, detail string) {
	kumaMu.Lock()
	defer kumaMu.Unlock()

	if !ok && !kumaFailing {
		kumaFailing = true
		log.Printf("push do Kuma falhou: %s (dead-man switch cego ate voltar)", detail)
	} else if ok && kumaFailing {
		kumaFailing = false
		log.Println("push do Kuma normalizado")
	}
}
