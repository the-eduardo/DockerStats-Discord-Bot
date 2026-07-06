// Command bot é o entrypoint do DockerStats Discord Bot.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/discord"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[dockerstats] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	dx, err := dockerx.New(cfg.Hostname)
	if err != nil {
		log.Fatalf("docker: %v", err)
	}
	defer dx.Close()

	// Falha cedo se o daemon do Docker não estiver acessível.
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := dx.Ping(pingCtx); err != nil {
		cancel()
		log.Fatalf("docker daemon inacessível: %v", err)
	}
	cancel()

	bot, err := discord.New(cfg, dx)
	if err != nil {
		log.Fatalf("discord: %v", err)
	}
	if err := bot.Start(); err != nil {
		log.Fatalf("start: %v", err)
	}
	log.Printf("Bot rodando em %q. Ctrl+C para sair.", cfg.Hostname)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("Encerrando...")
	bot.Stop()
}
