package discord

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO de render(), não hostEmbedWithList/componentsFrom
// isoladas: a dedução de coleta única só vale se o call site real (dashboard.go)
// de fato parar de listar os containers duas vezes por ciclo.

// listCountingHost sobe um stub mínimo do daemon Docker e conta quantas vezes
// /containers/json foi chamado — é o sinal que prova (ou derruba) a coleta
// única por render.
func listCountingHost(t *testing.T, hits *atomic.Int32) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"NCPU": 4, "MemTotal": int64(8 << 30)})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("host-a", "Host A")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// Cobre a regressão real medida em produção (log do socket-proxy, 29/08/2026):
// cada render() listava os containers de cada host DUAS vezes — uma via
// hostEmbed, outra via buildDashboardComponents — pagando o dobro de chamadas
// de rede por ciclo, caro em especial para hosts remotos via SSH.
func TestRenderColetaContainersUmaVezPorHost(t *testing.T) {
	d := newTestDashboard(t, &fakeDiscordTransport{})
	d.bot.cfg = &config.Config{DiskPath: "/"} // isLocal=true (único host) chama system.Collect
	var hits atomic.Int32
	d.bot.hosts = []*dockerx.Client{listCountingHost(t, &hits)}

	if !d.render() {
		t.Fatal("render() esperava sucesso (publicar o painel no fake do Discord)")
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("esperava 1 List (/containers/json) por host por render, vieram %d", got)
	}
}

// listFailingHost responde 500 em /containers/json -- reproduz "host inacessível
// no momento" sem depender de derrubar um host de verdade.
func listFailingHost(t *testing.T) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("host-b", "Host B")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// Prova que a refatoração não perdeu o tratamento de host inacessível: com o
// único host falhando List, render() não pode entrar em pânico, deve publicar
// mesmo assim (embed "offline") e o select deve virar o placeholder inerte —
// dashboardCollect só inclui um host em `hosts` quando err == nil (mesmo
// critério que buildDashboardComponents já usava com o `continue`).
func TestRenderComHostFalhandoPublicaPlaceholder(t *testing.T) {
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)
	d.bot.cfg = &config.Config{DiskPath: "/"} // isLocal=true (único host) chama system.Collect
	d.bot.hosts = []*dockerx.Client{listFailingHost(t)}

	if !d.render() {
		t.Fatal("render() esperava sucesso (painel publicado mesmo com host offline)")
	}
	if got := transport.postCount.Load(); got != 1 {
		t.Fatalf("esperava 1 POST de criação de painel, vieram %d", got)
	}
	if strings.Count(logBuf.String(), `hostEmbed "host-b":`) != 1 {
		t.Fatalf(`esperava exatamente 1 linha de log "hostEmbed \"host-b\":", veio: %q`, logBuf.String())
	}

	var payload struct {
		Embeds []struct {
			Description string `json:"description"`
		} `json:"embeds"`
		Components []struct {
			Components []struct {
				Options []struct {
					Value string `json:"value"`
				} `json:"options"`
			} `json:"components"`
		} `json:"components"`
	}
	lastBody := transport.lastPostBody()
	if err := json.Unmarshal(lastBody, &payload); err != nil {
		t.Fatalf("payload nao decodificou: %v; corpo: %s", err, lastBody)
	}
	if len(payload.Embeds) != 1 || payload.Embeds[0].Description != "Host inacessível no momento." {
		t.Fatalf("esperava embed 'offline' unico, veio: %+v", payload.Embeds)
	}
	if len(payload.Components) != 2 || len(payload.Components[0].Components) != 1 ||
		len(payload.Components[0].Components[0].Options) != 1 ||
		payload.Components[0].Components[0].Options[0].Value != "_none" {
		t.Fatalf("esperava select com o placeholder inerte '_none', veio: %+v", payload.Components)
	}
}
