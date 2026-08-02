package discord

import (
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
