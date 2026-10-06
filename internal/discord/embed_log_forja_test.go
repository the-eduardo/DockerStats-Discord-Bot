package discord

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// listFailingBodyHost é como listFailingHost/failingHost (render_collect_wiring_test.go),
// mas o 500 de /containers/json devolve um CORPO escolhido pelo chamador --
// os outros dois sempre respondem vazio, e aqui o corpo é o vetor do teste.
func listFailingBodyHost(t *testing.T, key, label, body string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(body))
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

// infoFailingBodyHost responde /containers/json normalmente (lista vazia) mas
// falha /info com o corpo escolhido pelo chamador -- exercita o segundo sítio
// de log (embed.go:72, só alcançado quando List funciona e Info falha).
func infoFailingBodyHost(t *testing.T, key, label, body string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(body))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
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

// TestHostEmbedLogInfoNaoPermiteForjarLinha é a irmã de
// TestHostEmbedLogNaoPermiteForjarLinha para o SEGUNDO sítio (embed.go:72,
// erro de c.Info em host remoto).
func TestHostEmbedLogInfoNaoPermiteForjarLinha(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	local := &dockerx.Client{Key: "main"}
	remote := infoFailingBodyHost(t, "master", "Master", "boom\nhostEmbed \"master\": Info: FORJADO")

	b := &Bot{hosts: []*dockerx.Client{local}}
	_ = b.hostEmbed(context.Background(), remote)

	saida := buf.String()
	if strings.Contains(saida, "\nhostEmbed") {
		t.Fatalf("corpo do erro de Info forjou linha de log: %q", saida)
	}
	if strings.Count(saida, "\n") != 1 {
		t.Fatalf("esperava exatamente 1 quebra de linha, veio %d em: %q", strings.Count(saida, "\n"), saida)
	}
}

// TestHostEmbedLogNaoPermiteForjarLinha prova o %q+truncate de hostEmbedWithList:
// o erro de c.List, em host remoto, embute o stderr do sshd/sudo -- texto que
// este bot não controla. Com %v cru, um "\n" nesse texto forja uma linha de log
// inteira. Mesma classe já fechada em logDenied (auth_denial_log_test.go,
// TestLogNegacaoNaoPermiteForjarLinha) e no %q do /logs (ops.go).
func TestHostEmbedLogNaoPermiteForjarLinha(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	local := &dockerx.Client{Key: "main"}
	remote := listFailingBodyHost(t, "master", "Master", "boom\nhostEmbed \"master\": Info: FORJADO")

	b := &Bot{hosts: []*dockerx.Client{local}}
	_ = b.hostEmbed(context.Background(), remote)

	saida := buf.String()
	if strings.Contains(saida, "\nhostEmbed") {
		t.Fatalf("corpo do erro forjou linha de log: %q", saida)
	}
	if strings.Count(saida, "\n") != 1 {
		t.Fatalf("esperava exatamente 1 quebra de linha (o terminador do log.Printf), veio %d em: %q", strings.Count(saida, "\n"), saida)
	}
}

// TestHostEmbedLogTruncaErroGigante prova o teto de 300 runes: um corpo de
// erro gigantesco (ex. uma página HTML de erro de proxy) não pode inflar a
// linha de log sem limite -- mesma preocupação do truncate() já usado em
// audit.go e em renderContainers.
func TestHostEmbedLogTruncaErroGigante(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	corpoGigante := strings.Repeat("x", 5000)
	local := &dockerx.Client{Key: "main"}
	remote := listFailingBodyHost(t, "master", "Master", corpoGigante)

	b := &Bot{hosts: []*dockerx.Client{local}}
	_ = b.hostEmbed(context.Background(), remote)

	saida := buf.String()
	if len([]rune(saida)) >= 400 {
		t.Fatalf("linha de log não foi truncada, tem %d runes: %q", len([]rune(saida)), saida)
	}
}
