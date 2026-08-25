// Package discord implementa o bot: sessão, registro de slash commands e
// roteamento de interações. Toda ação passa por checagem de OwnerID.
package discord

import (
	"context"
	"log"
	"sync"
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

	// auditWG conta as gravacoes de auditoria em voo. audit() e assincrono de
	// proposito (nao pode atrasar a acao principal), mas sem este contador o
	// Stop() fecha a sessao e o main retorna enquanto ainda ha POST pendente —
	// e o registro se perde EXATAMENTE no evento que mais importa: o SIGTERM de
	// um deploy, restart ou OOM. Achado do painel Dev Senior em 25/08/2026.
	auditWG sync.WaitGroup

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
	if err := b.openWithRetry(); err != nil {
		return err
	}

	// Falha cedo só se o host LOCAL estiver inacessível; remotos são resilientes.
	// Retry com backoff cobre o caso do socket-proxy ainda não ter subido num
	// boot a frio da VPS (ver claude-agents/local-triage, 2026-07-27).
	if err := b.pingLocalWithRetry(); err != nil {
		return err
	}

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

// pingLocalWithRetry tenta o Ping do host local com backoff curto (2s, 4s,
// 8s, 16s, 16s; teto ~40s), para tolerar o socket-proxy ainda subindo num
// boot a frio. Se todas as tentativas falharem, devolve o último erro.
func (b *Bot) pingLocalWithRetry() error {
	delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 16 * time.Second}
	var lastErr error
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		lastErr = b.localHost().Ping(ctx)
		cancel()
		if lastErr == nil {
			return nil
		}
		if attempt >= len(delays) {
			return lastErr
		}
		log.Printf("ping do host local falhou (tentativa %d/%d): %v — nova tentativa em %s", attempt+1, len(delays)+1, lastErr, delays[attempt])
		time.Sleep(delays[attempt])
	}
}

// openWithRetry abre a sessao do gateway com o mesmo backoff curto do ping
// local. Sem ele, um timeout de TLS transitorio na conexao inicial sobe ate o
// log.Fatalf de main.go e o container reinicia inteiro (visto em 28/07/2026);
// com o retry, a mesma falha custa alguns segundos de espera.
func (b *Bot) openWithRetry() error {
	delays := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 16 * time.Second}
	var lastErr error
	for attempt := 0; ; attempt++ {
		lastErr = b.session.Open()
		if lastErr == nil {
			return nil
		}
		if attempt >= len(delays) {
			return lastErr
		}
		log.Printf("abertura da sessao falhou (tentativa %d/%d): %v - nova tentativa em %s", attempt+1, len(delays)+1, lastErr, delays[attempt])
		time.Sleep(delays[attempt])
	}
}

// Stop para o painel, remove os comandos registrados e fecha tudo.
// esperaAuditoria bloqueia ate as gravacoes de auditoria em voo terminarem ou
// ate o prazo estourar. Devolve false no timeout — o chamador loga e segue, em
// vez de segurar o shutdown para sempre.
func esperaAuditoria(wg *sync.WaitGroup, prazo time.Duration) bool {
	pronto := make(chan struct{})
	go func() { defer close(pronto); wg.Wait() }()
	select {
	case <-pronto:
		return true
	case <-time.After(prazo):
		return false
	}
}

func (b *Bot) Stop() {
	b.dashboard.stop()
	b.unregisterCommands()
	// ANTES de fechar a sessao: o POST de auditoria usa a REST do discordgo, e
	// session.Close() derruba o gateway sem esperar requisicao em voo.
	if !esperaAuditoria(&b.auditWG, 5*time.Second) {
		log.Println("timeout esperando a auditoria pendente terminar de gravar")
	}
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
