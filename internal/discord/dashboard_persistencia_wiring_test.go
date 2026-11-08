package discord

import (
	"net/http"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/store"
)

// novoDashboardNoDir monta um Dashboard com o store apontando para dir --
// permite simular dois boots (dois *store.Store, duas *Dashboard) sobre o
// MESMO arquivo dashboard.json, como um restart real do container faria.
func novoDashboardNoDir(t *testing.T, dir string, tr *fakeDiscordTransport) *Dashboard {
	t.Helper()
	session, err := discordgo.New("Bot faketoken")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: tr}

	st, err := store.New(dir)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}

	bot := &Bot{session: session, store: st}
	return newDashboard(bot)
}

// TestPainelSobreviveAoRestartEditandoAMesmaMensagem prova a única função do
// pacote store (ver comentário em store.go:1-3): depois de um restart, o bot
// volta a EDITAR a mensagem-painel já existente, em vez de criar uma nova e
// deixar a antiga órfã. Sem isso, cada recreate/reboot duplica o painel e,
// com DASHBOARD_CHANNEL_ID vazio em produção, a referência perdida significa
// ficar sem painel até alguém rodar /dashboard de novo.
func TestPainelSobreviveAoRestartEditandoAMesmaMensagem(t *testing.T) {
	dir := t.TempDir()

	// Boot 1: cria o painel e persiste a referência em dir/dashboard.json.
	tr1 := &fakeDiscordTransport{}
	d1 := novoDashboardNoDir(t, dir, tr1)
	d1.channelID = "123"
	d1.render()
	if got := tr1.postCount.Load(); got != 1 {
		t.Fatalf("boot 1 esperava exatamente 1 POST de criação do painel, recebi %d", got)
	}

	// Boot 2: store NOVO apontando pro MESMO dir, como um restart do
	// container faria. Cravar DashboardChannelID aqui é de propósito: sem a
	// referência persistida, o bot cairia no fallback do canal do env e
	// faria POST -- o teste falha com uma mensagem clara ("criou painel
	// novo") em vez de um timeout genérico.
	tr2 := &fakeDiscordTransport{}
	d2 := novoDashboardNoDir(t, dir, tr2)
	d2.bot.cfg = &config.Config{RefreshInterval: time.Hour, DashboardChannelID: "123"}
	d2.start()
	defer stopAndJoinLoop(t, d2)

	deadline := time.Now().Add(3 * time.Second)
	for tr2.postCount.Load()+tr2.editCount.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := tr2.postCount.Load(); got != 0 {
		t.Fatalf("restart criou painel novo: a referência salva foi ignorada (%d POST)", got)
	}
	if got := tr2.editCount.Load(); got != 1 {
		t.Fatalf("restart deveria editar a mensagem salva exatamente 1 vez, editou %d", got)
	}

	d2.mu.Lock()
	gotID := d2.messageID
	d2.mu.Unlock()
	if gotID != "msg-1" {
		t.Fatalf("messageID após restart = %q, esperava %q (da referência persistida)", gotID, "msg-1")
	}
}
