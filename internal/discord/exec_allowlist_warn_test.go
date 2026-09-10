package discord

import (
	"log"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
)

// EXEC_ALLOWLIST vazia mantém o /exec irrestrito (execAllowed, ops.go:221-224)
// — um modo válido, mas silencioso: nada no boot, no log ou no painel dizia
// que a trava mais perigosa do bot estava desligada. Este teste exercita o
// CAMINHO REAL de boot (discord.New), não uma condição isolada: New() é
// chamável em teste sem rede (discordgo.New só monta struct, store.New só faz
// MkdirAll, NewLocal só monta o client, e sem cfg.Remotes não há NewRemote).

func newTestConfig(dataDir string) *config.Config {
	return &config.Config{
		Token:   "token-de-teste",
		OwnerID: "owner-de-teste",
		DataDir: dataDir,
	}
}

func TestNewAvisaNoBootQuandoAllowlistVazia(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	cfg := newTestConfig(t.TempDir())
	cfg.ExecAllowlist = nil

	if _, err := New(cfg); err != nil {
		t.Fatalf("New: %v", err)
	}

	if !strings.Contains(buf.String(), "EXEC_ALLOWLIST vazia") {
		t.Errorf("boot com allowlist vazia nao avisou; log: %q", buf.String())
	}
}

func TestNewFicaEmSilencioQuandoAllowlistPopulada(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")

	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)

	cfg := newTestConfig(t.TempDir())
	cfg.ExecAllowlist = []string{"ls"}

	if _, err := New(cfg); err != nil {
		t.Fatalf("New: %v", err)
	}

	if strings.Contains(buf.String(), "EXEC_ALLOWLIST") {
		t.Errorf("boot com allowlist populada avisou por engano; log: %q", buf.String())
	}
}
