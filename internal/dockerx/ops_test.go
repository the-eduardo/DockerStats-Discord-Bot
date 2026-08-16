package dockerx

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/docker/docker/pkg/stdcopy"
)

// TestTailWriterCapsMemory varre vários padrões de escrita e garante que o
// buffer nunca ultrapassa max, e que o conteúdo retido é sempre a CAUDA do
// que foi escrito — nunca o começo.
func TestTailWriterCapsMemory(t *testing.T) {
	const max = 64

	patterns := []struct {
		nome   string
		chunks []int // tamanhos de cada Write, em bytes
	}{
		{"muitas escritas pequenas", repeat(1, 500)},
		{"escritas do tamanho do stdcopy (~4KiB de payload real, aqui menor)", repeat(7, 40)},
		{"uma única escrita maior que max", []int{500}},
		{"escrita exatamente do tamanho de max", []int{max}},
		{"escrita 1 byte maior que max", []int{max + 1}},
		{"mistura de tamanhos", []int{3, 50, 1, 90, 2, 40}},
	}

	for _, p := range patterns {
		t.Run(p.nome, func(t *testing.T) {
			w := &tailWriter{max: max}
			var all []byte
			r := rand.New(rand.NewSource(42))
			for _, n := range p.chunks {
				chunk := make([]byte, n)
				for i := range chunk {
					chunk[i] = byte('a' + r.Intn(26))
				}
				written, err := w.Write(chunk)
				if err != nil {
					t.Fatalf("Write retornou erro: %v", err)
				}
				if written != n {
					t.Fatalf("short write: pedi %d, escreveu %d", n, written)
				}
				all = append(all, chunk...)
			}

			got := w.String()
			if len(got) > max {
				t.Fatalf("buffer excedeu o teto: len=%d max=%d", len(got), max)
			}

			want := all
			if len(want) > max {
				want = want[len(want)-max:]
			}
			if got != string(want) {
				t.Fatalf("conteúdo retido não é a cauda esperada\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

// TestTailWriterNeverShortWrites prova o contrato exigido pelo stdcopy:
// Write sempre devolve len(p), nil, mesmo quando o excedente é descartado.
// Um short write faria o stdcopy.StdCopy abortar no meio da leitura de logs.
func TestTailWriterNeverShortWrites(t *testing.T) {
	w := &tailWriter{max: 8}
	for _, n := range []int{1, 8, 9, 100, 0} {
		p := make([]byte, n)
		written, err := w.Write(p)
		if err != nil {
			t.Fatalf("Write(%d bytes) retornou erro: %v", n, err)
		}
		if written != n {
			t.Fatalf("Write(%d bytes) devolveu %d — short write quebraria o stdcopy.StdCopy", n, written)
		}
	}
}

// TestLogsWiringCapsStdcopyOutput exercita o MESMO caminho que Logs() usa no
// ramo sem TTY: stdcopy.StdCopy(tw, tw, rc). Codifica um stream real no
// formato de frame do Docker (via stdcopy.NewStdWriter) maior que o teto, e
// confirma que o resultado final fica limitado — não só a função pura
// tailWriter, mas a fiação com o demux que Logs() realmente usa.
func TestLogsWiringCapsStdcopyOutput(t *testing.T) {
	const max = 1024

	var encoded bytes.Buffer
	stdout := stdcopy.NewStdWriter(&encoded, stdcopy.Stdout)
	payload := make([]byte, max*4)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	if _, err := stdout.Write(payload); err != nil {
		t.Fatalf("falha ao codificar payload de teste: %v", err)
	}

	tw := &tailWriter{max: max}
	if _, err := stdcopy.StdCopy(tw, tw, &encoded); err != nil {
		t.Fatalf("stdcopy.StdCopy retornou erro: %v", err)
	}

	got := tw.String()
	if len(got) > max {
		t.Fatalf("stdcopy.StdCopy com tailWriter excedeu o teto: len=%d max=%d", len(got), max)
	}
	want := string(payload[len(payload)-max:])
	if got != want {
		t.Fatalf("conteúdo final não é a cauda esperada do stream demuxado\ngot:  %q\nwant: %q", got, want)
	}
}

func repeat(size, times int) []int {
	out := make([]int, times)
	for i := range out {
		out[i] = size
	}
	return out
}
