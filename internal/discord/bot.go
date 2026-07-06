// Package discord implementa o bot: sessão, registro de slash commands e
// roteamento de interações. Toda ação passa por checagem de OwnerID.
package discord

import (
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Bot agrega a sessão do Discord e as dependências (config + Docker).
type Bot struct {
	cfg     *config.Config
	dx      *dockerx.Client
	session *discordgo.Session

	registered []*discordgo.ApplicationCommand
}

// New cria o bot e registra o handler de interações.
func New(cfg *config.Config, dx *dockerx.Client) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, err
	}
	// Só precisamos de eventos de guild; sem intents privilegiados.
	s.Identify.Intents = discordgo.IntentsGuilds

	b := &Bot{cfg: cfg, dx: dx, session: s}
	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Conectado como %s#%s", r.User.Username, r.User.Discriminator)
	})
	s.AddHandler(b.onInteraction)
	return b, nil
}

// Start abre a conexão e registra os slash commands.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}
	return b.registerCommands()
}

// Stop remove os comandos registrados e fecha a conexão.
func (b *Bot) Stop() {
	b.unregisterCommands()
	if err := b.session.Close(); err != nil {
		log.Printf("erro ao fechar sessão: %v", err)
	}
}

// isOwner garante que apenas o dono configurado interaja com o bot.
func (b *Bot) isOwner(i *discordgo.InteractionCreate) bool {
	var userID string
	switch {
	case i.Member != nil && i.Member.User != nil:
		userID = i.Member.User.ID // interação em servidor
	case i.User != nil:
		userID = i.User.ID // interação em DM
	}
	return userID != "" && userID == b.cfg.OwnerID
}
