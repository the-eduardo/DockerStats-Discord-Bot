package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// listOneContainerHost sobe um stub do daemon Docker cujo /containers/json
// devolve UM container não-vazio (nome conhecido). Diferente de
// listCountingHost (lista vazia), este stub existe para provar que o dado
// coletado por dashboardCollect chega de fato ao select publicado por
// render() -- com host vazio o resultado é indistinguível do placeholder
// "_none" por construção (ver componentsFrom em components.go), então essa
// fiação só é provada com um container real na lista.
func listOneContainerHost(t *testing.T) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"Id":     "abc123",
					"Names":  []string{"/meu-container"},
					"Image":  "nginx",
					"State":  "exited", // exited: CollectStats não bate em /stats, sem stub extra
					"Status": "Exited (0) 2 minutes ago",
				},
			})
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"NCPU": 4, "MemTotal": int64(8 << 30)})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("host-c", "Host C")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// TestRenderSelectContemContainerColetadoPeloCiclo prova a fiação que falta:
// dashboardCollect() coleta os containers do ciclo e componentsFrom() usa
// ESSA MESMA coleta (não uma lista própria, nem nil) para montar o select
// publicado por render(). Sem esta fiação, dashboard.go poderia chamar
// d.bot.componentsFrom(nil) no lugar de componentsFrom(hosts) que render()
// já não listaria de novo, mas produziria o select vazio/placeholder mesmo
// com containers de verdade no host -- e TestRenderColetaContainersUmaVezPorHost
// (host sem containers) não detecta essa quebra, porque lista vazia e nil
// resultam no mesmo placeholder "_none" por construção.
func TestRenderSelectContemContainerColetadoPeloCiclo(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)
	d.bot.cfg = &config.Config{DiskPath: "/"} // isLocal=true (único host) chama system.Collect
	d.bot.hosts = []*dockerx.Client{listOneContainerHost(t)}

	if !d.render() {
		t.Fatal("render() esperava sucesso (publicar o painel no fake do Discord)")
	}

	var payload struct {
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
	if len(payload.Components) != 2 || len(payload.Components[0].Components) != 1 {
		t.Fatalf("payload de componentes com formato inesperado: %+v", payload.Components)
	}
	options := payload.Components[0].Components[0].Options
	if len(options) != 1 {
		t.Fatalf("esperava exatamente 1 opção no select (o container coletado), vieram %d: %+v", len(options), options)
	}
	if got, want := options[0].Value, target("host-c", "meu-container"); got != want {
		t.Fatalf("select publicado não contém o container coletado no ciclo: value=%q, queria %q (e não pode ser o placeholder %q)", got, want, "_none")
	}
}

// manyContainersHost sobe um stub cujo /containers/json devolve `n`
// containers nomeados "<prefix><NN>" (zero-padded, então a ordenação por
// nome que List() faz preserva a ordem de índice) -- usado para forçar
// buildSelectOptions a truncar por cota, o que só acontece com hosts
// suficientes e containers suficientes por host.
func manyContainersHost(t *testing.T, key, label, prefix string, n int) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			list := make([]map[string]any, 0, n)
			for i := 0; i < n; i++ {
				list = append(list, map[string]any{
					"Id":     fmt.Sprintf("%s-%02d", key, i),
					"Names":  []string{fmt.Sprintf("/%s%02d", prefix, i)},
					"Image":  "nginx",
					"State":  "exited", // exited: CollectStats não bate em /stats, sem stub extra
					"Status": "Exited (0) 2 minutes ago",
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"NCPU": 4, "MemTotal": int64(8 << 30)})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal(key, label)
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// failingHost é como listFailingHost, mas com key/label próprios -- para
// compor um cenário com hosts que respondem E um host que falha, sem colidir
// com o key fixo "host-b" que listFailingHost já usa em outro teste.
func failingHost(t *testing.T, key, label string) *dockerx.Client {
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
	h, err := dockerx.NewLocal(key, label)
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// TestDashboardCollectExcluiHostFalhoDaCotaDoSelect prova a guarda `err ==
// nil` de dashboardCollect (embed.go): um host cujo List() falhou NÃO pode
// entrar em `hosts` (nem com lista vazia), porque `hosts` é o denominador da
// cota em buildSelectOptions (components.go: quota = maxSelectOptions /
// len(hosts)). Um host "fantasma" (incluído porém sem containers) infla esse
// denominador e rouba cota dos hosts que de fato responderam -- com 1 só host
// essa mutação é absorvível por construção (host incluído-vazio e
// host-excluído dão o mesmo total), então este teste precisa de >= 2 hosts
// reais com mais containers do que a cota, mais um 3º host que falha.
func TestDashboardCollectExcluiHostFalhoDaCotaDoSelect(t *testing.T) {
	transport := &fakeDiscordTransport{}
	d := newTestDashboard(t, transport)
	d.bot.cfg = &config.Config{DiskPath: "/"} // isLocal=true só para b.hosts[0] (localHost())

	const perHost = 15 // > quota (25/2=12 correto; 25/3=8 mutado) para forcar truncamento
	hostA := manyContainersHost(t, "host-a2", "Host A2", "app-", perHost)
	hostB := manyContainersHost(t, "host-b2", "Host B2", "worker-", perHost)
	hostC := failingHost(t, "host-c2", "Host C2")
	d.bot.hosts = []*dockerx.Client{hostA, hostB, hostC}

	if !d.render() {
		t.Fatal("render() esperava sucesso (publicar o painel no fake do Discord)")
	}

	var payload struct {
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
	if len(payload.Components) != 2 || len(payload.Components[0].Components) != 1 {
		t.Fatalf("payload de componentes com formato inesperado: %+v", payload.Components)
	}
	options := payload.Components[0].Components[0].Options

	var countA, countB, countC int
	haveWorker10 := false
	for _, opt := range options {
		switch {
		case strings.HasPrefix(opt.Value, "host-a2:"):
			countA++
		case strings.HasPrefix(opt.Value, "host-b2:"):
			countB++
			if opt.Value == target("host-b2", "worker-10") {
				haveWorker10 = true
			}
		case strings.HasPrefix(opt.Value, "host-c2:"):
			countC++
		}
	}

	if countC != 0 {
		t.Fatalf("host-c2 falhou List() e não pode aparecer no select (guarda err==nil), mas apareceu %d vez(es)", countC)
	}
	// Com só os 2 hosts que responderam contando na cota (25/2=12), A leva 13
	// (12 da cota + 1 de sobra) e B leva 12 -- inclusive worker-10 (índice 10
	// < 12). Se o host-c2 falho entrar na conta (mutação `err == nil` ->
	// sempre true), a cota vira 25/3=8 e B fica só com worker-00..worker-09
	// (10 itens): worker-10 some e A absorve a sobra sozinho (15).
	if countA != 13 || countB != 12 || !haveWorker10 {
		t.Fatalf("distribuição de cota errada (host-c2 falho vazando pro denominador): A=%d B=%d worker-10 presente=%v (esperava A=13 B=12 worker-10=true)",
			countA, countB, haveWorker10)
	}
}
