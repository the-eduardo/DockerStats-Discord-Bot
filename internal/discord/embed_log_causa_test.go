package discord

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// TestHostEmbedLogPreservaCausaNoFim prova que o teto do log de hostEmbed
// (embed.go:34) preserva o FIM do erro, nao so o prefixo: no erro real de
// commandconn do docker/cli a causa (stderr=...) vem depois do argv do ssh,
// e o truncate() antigo (so prefixo) descartava exatamente essa parte.
func TestHostEmbedLogPreservaCausaNoFim(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	corpo := strings.Repeat("x", 5000) + "stderr=CAUSA_REAL"
	local := &dockerx.Client{Key: "main"}
	remote := listFailingBodyHost(t, "master", "Master", corpo)

	b := &Bot{hosts: []*dockerx.Client{local}}
	_ = b.hostEmbed(context.Background(), remote)

	saida := buf.String()
	if !strings.Contains(saida, "CAUSA_REAL") {
		t.Fatalf("causa real (stderr=...) nao sobreviveu ao teto: %q", saida)
	}
	if !strings.Contains(saida, "Error response") && !strings.Contains(saida, "hostEmbed") {
		t.Fatalf("inicio do erro nao sobreviveu ao teto: %q", saida)
	}
	if len([]rune(saida)) >= 400 {
		t.Fatalf("linha de log nao foi truncada, tem %d runes: %q", len([]rune(saida)), saida)
	}
}

// TestHostEmbedLogInfoPreservaCausaNoFim e a irma da anterior para o sitio
// de Info (embed.go:72).
func TestHostEmbedLogInfoPreservaCausaNoFim(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	corpo := strings.Repeat("x", 5000) + "stderr=CAUSA_REAL"
	local := &dockerx.Client{Key: "main"}
	remote := infoFailingBodyHost(t, "master", "Master", corpo)

	b := &Bot{hosts: []*dockerx.Client{local}}
	_ = b.hostEmbed(context.Background(), remote)

	saida := buf.String()
	if !strings.Contains(saida, "CAUSA_REAL") {
		t.Fatalf("causa real (stderr=...) nao sobreviveu ao teto de Info: %q", saida)
	}
	if !strings.Contains(saida, "Info:") {
		t.Fatalf("ramo de Info nao exercitado: %q", saida)
	}
	if len([]rune(saida)) >= 400 {
		t.Fatalf("linha de log de Info nao truncada, %d runes", len([]rune(saida)))
	}
}

// TestTruncateMiddle cobre truncateMiddle isoladamente: string curta intacta,
// limite exato head+tail, corte multibyte sem partir rune, e o formato real
// observado em producao (erro de commandconn com stderr no fim).
func TestTruncateMiddle(t *testing.T) {
	casos := []struct {
		nome       string
		s          string
		head, tail int
		querCausa  string // substring que precisa sobreviver
	}{
		{
			nome: "string curta fica intacta",
			s:    "erro pequeno",
			head: 60, tail: 240,
		},
		{
			nome: "limite exato head+tail nao corta",
			s:    strings.Repeat("a", 300),
			head: 60, tail: 240,
		},
		{
			nome: "multibyte sem corte de rune",
			s:    strings.Repeat("á", 5000) + "stderr=FIM_çãéíóú",
			head: 60, tail: 240,
			querCausa: "FIM_çãéíóú",
		},
		{
			nome: "formato real de producao com host key verification",
			s:    "command [ssh -i /root/.ssh/id_ed25519 ubuntu@10.0.0.5 docker system dial-stdio] has exited with 255, make sure the URL is valid, and Docker 18.09 or later is installed on the remote host: stderr=" + strings.Repeat("x", 4096) + "Host key verification failed.",
			head: 60, tail: 240,
			querCausa: "Host key verification failed.",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			out := truncateMiddle(c.s, c.head, c.tail)
			if len([]rune(c.s)) <= c.head+c.tail {
				if out != c.s {
					t.Fatalf("string dentro do limite foi alterada: %q -> %q", c.s, out)
				}
				return
			}
			if c.querCausa != "" && !strings.Contains(out, c.querCausa) {
				t.Fatalf("causa esperada %q nao sobreviveu: %q", c.querCausa, out)
			}
			for _, r := range out {
				if r == '�' {
					t.Fatalf("corte partiu uma rune multibyte: %q", out)
				}
			}
		})
	}
}
