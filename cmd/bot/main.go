// Command bot é o entrypoint do DockerStats Discord Bot.
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/discord"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[dockerstats] ")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	bot, err := discord.New(cfg)
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
