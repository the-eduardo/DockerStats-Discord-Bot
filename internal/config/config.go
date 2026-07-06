// Package config carrega e valida a configuração do bot a partir de variáveis
// de ambiente. Diferente da versão antiga, é lido UMA vez no boot (não a cada
// mensagem) e o timeout de shutdown não é mais sobrescrito por engano.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config guarda todas as configurações do bot. Campos marcados como "Fase 2"
// já existem para não precisar mexer no schema depois, mas ainda não são usados.
type Config struct {
	Token    string // DISCORD_TOKEN (obrigatório)
	OwnerID  string // DISCORD_OWNER_ID (obrigatório) — único usuário autorizado
	GuildID  string // DISCORD_GUILD_ID (opcional) — se setado, registra comandos no servidor (instantâneo)
	Hostname string // HOSTNAME — rótulo da máquina exibido no embed

	ShutdownTimeout time.Duration // SHUTDOWN_TIMEOUT (segundos) para stop/restart graceful
	DiskPath        string        // DISK_PATH — caminho monitorado para uso de disco do host

	// Dashboard persistente (Fase 2).
	DashboardChannelID string        // DASHBOARD_CHANNEL_ID — canal inicial do painel (opcional; /dashboard também fixa)
	RefreshInterval    time.Duration // REFRESH_SECONDS — intervalo de atualização do painel
	DataDir            string        // DATA_DIR — onde persistir a referência do painel

	// Multi-host (Fase 4).
	Remotes []RemoteSpec // REMOTE_HOSTS — hosts Docker remotos via SSH
}

// RemoteSpec descreve um host Docker remoto.
type RemoteSpec struct {
	Key    string // id estável (ex.: "master")
	Label  string // nome amigável (ex.: "Oracle Master")
	Host   string // ex.: "ssh://ubuntu@1.2.3.4"
	SSHKey string // caminho da chave privada (opcional)
}

// parseRemotes lê REMOTE_HOSTS. Formato: entradas separadas por ";", campos por
// ",": "key,Label,ssh://user@ip[,/caminho/da/chave]".
func parseRemotes(raw string) []RemoteSpec {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []RemoteSpec
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		f := strings.Split(entry, ",")
		if len(f) < 3 {
			continue // entrada malformada; ignora
		}
		spec := RemoteSpec{
			Key:   strings.TrimSpace(f[0]),
			Label: strings.TrimSpace(f[1]),
			Host:  strings.TrimSpace(f[2]),
		}
		if len(f) >= 4 {
			spec.SSHKey = strings.TrimSpace(f[3])
		}
		if spec.Key != "" && spec.Host != "" {
			out = append(out, spec)
		}
	}
	return out
}

// Load lê o ambiente e valida os campos obrigatórios.
func Load() (*Config, error) {
	c := &Config{
		Token:              os.Getenv("DISCORD_TOKEN"),
		OwnerID:            os.Getenv("DISCORD_OWNER_ID"),
		GuildID:            os.Getenv("DISCORD_GUILD_ID"),
		Hostname:           envOr("HOSTNAME", "Machine"),
		DiskPath:           envOr("DISK_PATH", "/"),
		DashboardChannelID: os.Getenv("DASHBOARD_CHANNEL_ID"),
		DataDir:            envOr("DATA_DIR", "/app/data"),
		Remotes:            parseRemotes(os.Getenv("REMOTE_HOSTS")),
	}

	if c.Token == "" || c.OwnerID == "" {
		return nil, fmt.Errorf("DISCORD_TOKEN e DISCORD_OWNER_ID precisam estar definidos")
	}

	// Corrige o bug da versão antiga: aqui o default só é aplicado quando o
	// valor é inválido, e nunca é sobrescrito por 0 logo em seguida.
	c.ShutdownTimeout = time.Duration(envInt("SHUTDOWN_TIMEOUT", 10, 0, 300)) * time.Second
	c.RefreshInterval = time.Duration(envInt("REFRESH_SECONDS", 60, 10, 3600)) * time.Second

	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envInt lê um inteiro do ambiente, retornando def quando ausente/ inválido ou
// fora do intervalo [min, max].
func envInt(key string, def, min, max int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v < min || v > max {
		return def
	}
	return v
}
