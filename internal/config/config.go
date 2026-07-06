// Package config carrega e valida a configuração do bot a partir de variáveis
// de ambiente. Diferente da versão antiga, é lido UMA vez no boot (não a cada
// mensagem) e o timeout de shutdown não é mais sobrescrito por engano.
package config

import (
	"fmt"
	"os"
	"strconv"
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

	// Fase 2 (dashboard persistente) — ainda não utilizados.
	DashboardChannelID string        // DASHBOARD_CHANNEL_ID
	RefreshInterval    time.Duration // REFRESH_SECONDS
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
