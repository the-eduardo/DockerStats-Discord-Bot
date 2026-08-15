package discord

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// Dashboard gerencia a mensagem-painel persistente: cria, edita a cada
// RefreshInterval e recria caso a mensagem seja apagada.
type Dashboard struct {
	bot  *Bot
	mu   sync.Mutex
	done chan struct{}
	once sync.Once

	// renderMu serializa render(): sem ela, duas chamadas concorrentes com
	// messageID == "" (ex.: moveTo zerando o id enquanto o ticker dispara)
	// caem as duas no ramo de criação e publicam painel duplicado.
	renderMu sync.Mutex

	channelID string
	messageID string
}

func newDashboard(b *Bot) *Dashboard {
	return &Dashboard{bot: b, done: make(chan struct{})}
}

// start carrega a referência salva (ou o canal configurado) e sobe o loop.
// O loop roda mesmo sem canal definido: assim /dashboard pode ativá-lo depois.
func (d *Dashboard) start() {
	ref, err := d.bot.store.Load()
	if err != nil {
		log.Printf("dashboard: erro ao carregar referência: %v", err)
	}

	d.mu.Lock()
	d.channelID = ref.ChannelID
	d.messageID = ref.MessageID
	if d.channelID == "" {
		d.channelID = d.bot.cfg.DashboardChannelID // fallback para o canal do env
	}
	hasChannel := d.channelID != ""
	d.mu.Unlock()

	if !hasChannel {
		log.Println("dashboard: nenhum canal definido; rode /dashboard num canal para fixar o painel")
	}
	go d.loop()
}

// stop encerra o loop (idempotente).
func (d *Dashboard) stop() {
	d.once.Do(func() { close(d.done) })
}

// loop renderiza imediatamente e depois a cada RefreshInterval.
func (d *Dashboard) loop() {
	d.render()
	t := time.NewTicker(d.bot.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
			// push do Kuma so apos render OK (prova que o Discord aceitou a chamada)
			if d.render() {
				pushKuma()
			}
		}
	}
}

// moveTo fixa o painel em outro canal (comando /dashboard) e renderiza na hora.
func (d *Dashboard) moveTo(channelID string) {
	d.mu.Lock()
	d.channelID = channelID
	d.messageID = "" // força criar uma nova mensagem no canal novo
	d.mu.Unlock()
	d.render()
}

// render monta embed + componentes e edita (ou cria) a mensagem-painel.
// render devolve true quando o painel foi de fato criado/editado no
// Discord -- e o sinal usado pelo dead-man switch do Kuma (ver kuma.go).
func (d *Dashboard) render() bool {
	d.renderMu.Lock()
	defer d.renderMu.Unlock()

	d.mu.Lock()
	channelID, messageID := d.channelID, d.messageID
	d.mu.Unlock()

	if channelID == "" {
		return false // ainda não há canal fixado
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	embeds := d.bot.dashboardEmbeds(ctx)
	components := d.bot.buildDashboardComponents(ctx)
	s := d.bot.session

	// Sem mensagem ainda: cria e persiste a referência.
	if messageID == "" {
		msg, err := s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Embeds:     embeds,
			Components: components,
		})
		if err != nil {
			log.Printf("dashboard: erro ao criar painel: %v", err)
			return false
		}
		d.setMessage(channelID, msg.ID)
		return true
	}

	// Edita a mensagem existente.
	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &embeds,
		Components: &components,
	})
	if err != nil {
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Message != nil &&
			restErr.Message.Code == discordgo.ErrCodeUnknownMessage {
			// Mensagem foi de fato apagada: zera o id para recriar no próximo ciclo.
			log.Printf("dashboard: painel apagado, recriando: %v", err)
			d.mu.Lock()
			d.messageID = ""
			d.mu.Unlock()
		} else {
			// Erro transitório (5xx, timeout, rate limit): a mensagem ainda existe,
			// zerar o id aqui deixaria o painel antigo órfão e criaria um duplicado.
			log.Printf("dashboard: erro transitorio ao editar painel, tentando de novo no proximo ciclo: %v", err)
		}
	}
	return err == nil
}

// refreshNow dispara um render fora do ciclo (usado pelo botão Atualizar e
// após ações de start/stop/restart).
func (d *Dashboard) refreshNow() {
	go func() {
		if !d.renderMu.TryLock() {
			return // já há render em voo; ele publicará o estado atual
		}
		d.renderMu.Unlock()
		d.render()
	}()
}

// setMessage atualiza a referência em memória e no disco.
func (d *Dashboard) setMessage(channelID, messageID string) {
	d.mu.Lock()
	d.channelID = channelID
	d.messageID = messageID
	d.mu.Unlock()

	if err := d.bot.store.Save(store.DashboardRef{ChannelID: channelID, MessageID: messageID}); err != nil {
		log.Printf("dashboard: erro ao persistir referência: %v", err)
	}
}
