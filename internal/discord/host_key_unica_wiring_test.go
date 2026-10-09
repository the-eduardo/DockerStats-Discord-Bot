package discord

import (
	"log"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// A Key de host é o ENDEREÇO de toda ação perigosa: ela vai no Value do
// select, no custom_id dos botões (act:<verbo>:<hostKey>:<container>) e no
// CustomID do modal do /exec. hostByKey devolve o PRIMEIRO match e
// parseTarget corta no 1º ":" — Key repetida (inclusive "main", a do host
// local) ou com ":" desvia stop/restart/exec para o host ERRADO, em silêncio.
// Este teste exercita o caminho REAL de boot (discord.New): NewRemote só
// monta struct (dockerx/client.go), o ssh só nasce no 1º dial, então roda
// sem rede, como os 3 testes vizinhos de exec_allowlist_warn_test.go.

func bootComLog(t *testing.T, cfg *config.Config) (*Bot, string) {
	t.Helper()
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	b, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b, buf.String()
}

func TestNewIgnoraRemotoComKeyDoHostLocal(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Remotes = []config.RemoteSpec{{Key: "main", Label: "Outro", Host: "ssh://u@192.0.2.1"}}

	b, logged := bootComLog(t, cfg)

	if len(b.hosts) != 1 {
		t.Fatalf("esperava 1 host (so o local), got %d", len(b.hosts))
	}
	if !strings.Contains(logged, "ignorado") {
		t.Errorf("boot nao avisou da Key duplicada com o host local; log: %q", logged)
	}
}

func TestNewIgnoraRemotoComKeyRepetida(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Remotes = []config.RemoteSpec{
		{Key: "master", Label: "Primeiro", Host: "ssh://u@192.0.2.1"},
		{Key: "master", Label: "Segundo", Host: "ssh://u@192.0.2.2"},
	}

	b, logged := bootComLog(t, cfg)

	if len(b.hosts) != 2 {
		t.Fatalf("esperava 2 hosts (local + 1 remoto), got %d", len(b.hosts))
	}
	h := b.hostByKey("master")
	if h == nil || h.Label != "Primeiro" {
		t.Errorf("hostByKey(\"master\") deveria devolver o PRIMEIRO remoto, got %+v", h)
	}
	if !strings.Contains(logged, "ignorado") {
		t.Errorf("boot nao avisou da Key repetida; log: %q", logged)
	}
}

func TestNewIgnoraRemotoComDoisPontosNaKey(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Remotes = []config.RemoteSpec{{Key: "a:b", Label: "Invalido", Host: "ssh://u@192.0.2.1"}}

	b, logged := bootComLog(t, cfg)

	if len(b.hosts) != 1 {
		t.Fatalf("esperava 1 host (so o local), got %d", len(b.hosts))
	}
	if !strings.Contains(logged, "ignorado") {
		t.Errorf("boot nao avisou da Key com ':'; log: %q", logged)
	}
}

// TestTodoHostEEnderecavelPelaPropriaKey e' a FIACAO: e' o caminho que TODOS
// os handlers de acao usam (handleAction/handleSelect/handleModal) para
// resolver host a partir do custom_id/Value. Apagar a guarda acima deixa
// este teste passando do mesmo jeito se ele so checar host != nil — por isso
// afirma a IGUALDADE do ponteiro e o round-trip de target()/parseTarget().
func TestTodoHostEEnderecavelPelaPropriaKey(t *testing.T) {
	cfg := newTestConfig(t.TempDir())
	cfg.Remotes = []config.RemoteSpec{
		{Key: "master", Label: "Valido", Host: "ssh://u@192.0.2.1"},
		{Key: "master", Label: "Duplicado", Host: "ssh://u@192.0.2.2"},
		{Key: "a:b", Label: "ComDoisPontos", Host: "ssh://u@192.0.2.3"},
	}

	b, _ := bootComLog(t, cfg)

	if len(b.hosts) != 2 {
		t.Fatalf("esperava 2 hosts (local + 1 remoto valido), got %d", len(b.hosts))
	}
	for _, h := range b.hosts {
		if got := b.hostByKey(h.Key); got != h {
			t.Errorf("hostByKey(%q) nao devolveu o proprio host: got %+v, want %+v", h.Key, got, h)
		}
		k, name := parseTarget(target(h.Key, "x"))
		if k != h.Key || name != "x" {
			t.Errorf("target/parseTarget nao fez round-trip para Key %q: got (%q,%q)", h.Key, k, name)
		}
	}
}
