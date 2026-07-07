// Package discord implementa o bot: sessão, registro de slash commands e
// roteamento de interações. Toda ação passa por checagem de OwnerID.
package discord

import (
	"context"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// Bot agrega a sessão do Discord e as dependências (config + hosts + painel).
type Bot struct {
	cfg     *config.Config
	hosts   []*dockerx.Client // [0] é sempre o host local
	session *discordgo.Session
	store   *store.Store

	dashboard *Dashboard
	confirms  *confirmManager
	limiter   *rateLimiter

	registered []*discordgo.ApplicationCommand
}

// New cria o bot, os clients Docker (local + remotos), o store e o painel.
func New(cfg *config.Config) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuilds

	st, err := store.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	local, err := dockerx.NewLocal("main", cfg.Hostname)
	if err != nil {
		return nil, err
	}
	hosts := []*dockerx.Client{local}
	for _, r := range cfg.Remotes {
		rc, err := dockerx.NewRemote(r.Key, r.Label, r.Host, r.SSHKey)
		if err != nil {
			log.Printf("host remoto %q ignorado: %v", r.Key, err)
			continue
		}
		hosts = append(hosts, rc)
		log.Printf("host remoto adicionado: %s (%s)", r.Key, r.Host)
	}

	b := &Bot{cfg: cfg, hosts: hosts, session: s, store: st}
	b.dashboard = newDashboard(b)
	b.confirms = newConfirmManager(b)
	b.limiter = newRateLimiter(8, 0.5) // rajada de 8, ~30 ações/min

	s.AddHandler(func(_ *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Conectado como %s#%s", r.User.Username, r.User.Discriminator)
	})
	s.AddHandler(b.onInteraction)
	return b, nil
}

// localHost devolve o client do host local.
func (b *Bot) localHost() *dockerx.Client { return b.hosts[0] }

// hostByKey busca um host pela sua Key (retorna nil se não existir).
func (b *Bot) hostByKey(key string) *dockerx.Client {
	if key == "" {
		return b.localHost()
	}
	for _, h := range b.hosts {
		if h.Key == key {
			return h
		}
	}
	return nil
}

// Start abre a conexão, registra os slash commands e sobe o loop do painel.
func (b *Bot) Start() error {
	if err := b.session.Open(); err != nil {
		return err
	}

	// Falha cedo só se o host LOCAL estiver inacessível; remotos são resilientes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := b.localHost().Ping(ctx); err != nil {
		cancel()
		return err
	}
	cancel()

	// Ping (não fatal) dos hosts remotos, só para registrar o estado no log.
	for _, h := range b.hosts[1:] {
		pctx, pcancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := h.Ping(pctx); err != nil {
			log.Printf("host remoto %q INACESSÍVEL: %v", h.Key, err)
		} else {
			log.Printf("host remoto %q OK", h.Key)
		}
		pcancel()
	}

	if err := b.registerCommands(); err != nil {
		return err
	}
	b.dashboard.start()
	return nil
}

// Stop para o painel, remove os comandos registrados e fecha tudo.
func (b *Bot) Stop() {
	b.dashboard.stop()
	b.unregisterCommands()
	if err := b.session.Close(); err != nil {
		log.Printf("erro ao fechar sessão: %v", err)
	}
	for _, h := range b.hosts {
		_ = h.Close()
	}
}

// isOwner garante que apenas o dono configurado interaja com o bot.
func (b *Bot) isOwner(i *discordgo.InteractionCreate) bool {
	var userID string
	switch {
	case i.Member != nil && i.Member.User != nil:
		userID = i.Member.User.ID
	case i.User != nil:
		userID = i.User.ID
	}
	return userID != "" && userID == b.cfg.OwnerID
}
