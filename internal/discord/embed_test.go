package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Primeiro teste do repo. Cobre a regressao real que motivou o fix de 02/08/2026:
// renderContainers truncava com `out[:1000]`, um slice de BYTE. Como cada linha
// comeca com um emoji de estado (multibyte), o corte podia cair no meio de um
// emoji e o painel do Discord mostrava o caractere de substituicao. Com 21
// containers neste host a lista ja passa dos 1024 chars, entao o caminho de
// truncamento nao e' hipotetico — e' o caso normal.
func containersDeTeste(n int, nome string) []dockerx.Container {
	list := make([]dockerx.Container, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, dockerx.Container{
			Name:       nome,
			State:      "running",
			CPUPercent: 1.5,
			MemUsage:   1024 * 1024,
		})
	}
	return list
}

func TestRenderContainersTruncaSemQuebrarEmoji(t *testing.T) {
	// Duas escolhas deliberadas aqui, ambas aprendidas errando:
	//
	// 1. Varre comprimentos de nome em vez de fixar um cenario. Com `out[:1000]`
	//    o corte cai sempre no mesmo byte; se aquele byte for ASCII, o teste
	//    passa com o bug presente.
	// 2. Usa nome ACENTUADO (2 bytes por caractere). Com nome ASCII, o layout
	//    faz o byte 1000 nunca cair dentro dos 4 bytes do emoji — varrer 40
	//    tamanhos ainda passava com o bug. Com metade dos bytes do nome sendo
	//    continuacao de sequencia multibyte, o corte por byte quebra o encoding
	//    em varias das iteracoes.
	//
	// O contrato testado e' geral: truncar NUNCA pode partir um caractere.
	for tamanho := 1; tamanho <= 40; tamanho++ {
		nome := strings.Repeat("é", tamanho)
		out := renderContainers(containersDeTeste(30, nome))

		if !strings.Contains(out, "(lista truncada)") {
			t.Fatalf("nome de %d chars: esperava truncamento e nao houve", tamanho)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("nome de %d chars: saida nao e' UTF-8 valido — o corte partiu um caractere multibyte", tamanho)
		}
		if strings.ContainsRune(out, utf8.RuneError) {
			t.Fatalf("nome de %d chars: saida contem o caractere de substituicao (�), emoji partido pelo corte", tamanho)
		}
	}
}

func TestRenderContainersNaoTruncaListaCurta(t *testing.T) {
	out := renderContainers(containersDeTeste(2, "saki-bot"))

	if strings.Contains(out, "(lista truncada)") {
		t.Fatal("lista curta nao devia ser truncada")
	}
	if strings.Count(out, "saki-bot") != 2 {
		t.Fatalf("esperava os 2 containers na saida, veio: %q", out)
	}
}

func TestRenderContainersListaVazia(t *testing.T) {
	if got := renderContainers(nil); got != "_nenhum container encontrado_" {
		t.Fatalf("mensagem de lista vazia mudou: %q", got)
	}
}

// remoteStubHost sobe um daemon Docker fake que responde ping e lista vazia de
// containers, mas falha o /info quando infoOK é false — reproduz o caso real
// (janela do reboot 03h do master) em que List funciona e Info não.
func remoteStubHost(t *testing.T, key string, infoOK bool) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/info"):
			if !infoOK {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"NCPU": 4, "MemTotal": int64(8 << 30)})
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal(key, "Host de teste")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

// Cobre a regressão real que motivou o fix de 20/08/2026: hostEmbed engolia o
// erro de c.Info(ctx) em silêncio (embed.go:56-62) quando o host remoto
// responde List mas falha Info — janela real observada no log de produção às
// 03:00 UTC (reboot do master). Sem log, essa falha é invisível: o embed some
// os campos de CPU/RAM e ninguém percebe o motivo.
func TestHostEmbedRemotoComInfoFalhandoLogaErro(t *testing.T) {
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	local := &dockerx.Client{Key: "main"} // só usado por localHost() para comparar Key
	remote := remoteStubHost(t, "master", false)

	b := &Bot{hosts: []*dockerx.Client{local}}

	embed := b.hostEmbed(context.Background(), remote)

	if embed == nil {
		t.Fatal("hostEmbed devolveu nil")
	}
	if !strings.Contains(logBuf.String(), `hostEmbed "master": Info:`) {
		t.Fatalf("esperava log do erro de Info (hostEmbed \"master\": Info: ...), veio: %q", logBuf.String())
	}
	for _, f := range embed.Fields {
		if strings.Contains(f.Name, "CPUs") || strings.Contains(f.Name, "RAM total") {
			t.Fatalf("campo de CPU/RAM não deveria existir quando Info falha, veio: %+v", f)
		}
	}
	if embed.Footer == nil || !strings.Contains(embed.Footer.Text, "host remoto") {
		t.Fatalf("footer inesperado: %+v", embed.Footer)
	}
}

// Contraprova: com Info funcionando, os campos de CPU/RAM aparecem e nenhum
// erro é logado — garante que o teste acima não está apenas verificando
// ausência de crash.
func TestHostEmbedRemotoComInfoOKNaoLogaEPreencheCampos(t *testing.T) {
	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	local := &dockerx.Client{Key: "main"}
	remote := remoteStubHost(t, "master", true)

	b := &Bot{hosts: []*dockerx.Client{local}}

	embed := b.hostEmbed(context.Background(), remote)

	if logBuf.Len() != 0 {
		t.Fatalf("nao esperava log com Info funcionando, veio: %q", logBuf.String())
	}
	var gotCPU, gotRAM bool
	for _, f := range embed.Fields {
		if strings.Contains(f.Name, "CPUs") {
			gotCPU = true
		}
		if strings.Contains(f.Name, "RAM total") {
			gotRAM = true
		}
	}
	if !gotCPU || !gotRAM {
		t.Fatalf("esperava campos de CPU e RAM total preenchidos, veio: %+v", embed.Fields)
	}
}
