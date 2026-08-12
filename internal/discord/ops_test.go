package discord

import (
	"strings"
	"testing"
)

// tailBytes vira o guardrail do /logs contra o teto de upload do Discord
// (~8 MiB por bot/webhook). Os 3 casos cobrem: nada muda quando cabe, o
// resultado nunca passa do teto quando não cabe, e o corte nunca parte uma
// linha ao meio.
func TestTailBytesReturnsUnchangedWhenUnderLimit(t *testing.T) {
	s := "linha curta\nde log\n"
	if got := tailBytes(s, 1<<20); got != s {
		t.Errorf("tailBytes alterou string que já cabia no limite: %q", got)
	}
}

func TestTailBytesBoundsOutputAndKeepsTheEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200_000; i++ {
		b.WriteString("linha de log numero razoavelmente longa para encher o buffer\n")
	}
	s := b.String() // ~9.4 MiB
	const max = 7 << 20

	got := tailBytes(s, max)
	if len(got) > max+200 { // folga só pro prefixo "…(truncado...)\n"
		t.Fatalf("resultado com %d bytes, quer <= %d", len(got), max+200)
	}
	if !strings.HasSuffix(got, s[len(s)-100:]) {
		t.Error("resultado não termina com o final do log original")
	}
}

func TestTailBytesNeverSplitsALineInHalf(t *testing.T) {
	// Corte cru cairia no meio da 2a linha; a versão correta avança até o \n.
	s := "AAAA\nBBBBBBBBBBBBBBBBBBBB\nCCCC\n"
	got := tailBytes(s, 15)
	if !strings.HasPrefix(got, "…(truncado") {
		t.Fatalf("esperava prefixo de truncamento, veio: %q", got)
	}
	if strings.Contains(got, "B") {
		t.Errorf("corte incluiu parte da linha B em vez de avançar até o \\n seguinte: %q", got)
	}
	if !strings.HasSuffix(got, "CCCC\n") {
		t.Errorf("corte não preservou a última linha inteira: %q", got)
	}
}
