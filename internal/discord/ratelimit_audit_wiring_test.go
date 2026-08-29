package discord

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do redesenho da auditoria de recusa por rate
// limit: um embed por JANELA, não um por recusa. A versão 1-para-1 (proposta
// de 25/08/2026, nunca mesclada) amplificava exatamente a rajada que o
// limiter está contendo — 30 cliques recusados viravam 30 embeds no canal, e
// justamente numa rajada de 30 uma fila dessas não drenava no prazo de 5s do
// Stop() (bot.go). Aqui a mesma rajada tem que virar UM embed com a
// contagem.

// canalQueNuncaFecha neutraliza o timer real da janela de agregação: o
// teste dispara o flush na mão, no momento que quer.
func canalQueNuncaFecha(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

func TestHandleActionAgregaRecusaPorRateLimitNumaJanela(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.refusals.after = canalQueNuncaFecha
	b.limiter = newRateLimiter(0, 0) // balde vazio: toda ação é recusada

	for n := 0; n < 30; n++ {
		b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	}

	// Janela ainda aberta (o canal de teste nunca fecha): nada publicado
	// no canal de auditoria ainda.
	if strings.Contains(string(rt.all()), "rate-limit") {
		t.Fatal("auditoria publicou antes da janela fechar")
	}

	b.flushRefusals()
	b.auditWG.Wait()

	sent := string(rt.all())
	if n := strings.Count(sent, "rate-limit"); n != 1 {
		t.Fatalf("esperava exatamente 1 embed agregado de rate-limit, vieram %d; corpo: %q", n, sent)
	}
	// Contains cru em "30" pega o "30" de dentro de "3066993" (colorOK) e
	// passaria mesmo com a contagem errada — o texto inteiro da mensagem é o
	// único jeito de provar QUAL número foi gravado.
	if !strings.Contains(sent, "30 ação(ões) recusada(s) por rate limit em 16s") {
		t.Fatalf("embed agregado não menciona a contagem 30: %q", sent)
	}
}

// refusalModalInteraction monta a interação de submissão do modal de /exec,
// no formato que handleModal espera (CustomID "exec:<hostKey>:<container>",
// valor do campo "cmd" dentro de um ActionsRow/TextInput). O teste nunca
// alcança host.Exec (a recusa por rate limit sai antes), então dispensa o
// stub de hijack que exec_audit_wiring_test.go precisa para o caminho feliz.
func refusalModalInteraction(cmd string) *discordgo.InteractionCreate {
	data := discordgo.ModalSubmitInteractionData{
		CustomID: "exec:main:web",
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.TextInput{CustomID: "cmd", Value: cmd},
			}},
		},
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		ID:   "1", AppID: "2", Token: "tok",
		Data: data,
	}}
}

// newRefusalExecWiringBot monta um Bot mínimo capaz de rodar handleModal até
// a checagem de rate limit (sem stub de exec/hijack — ver comentário de
// refusalModalInteraction).
func newRefusalExecWiringBot(t *testing.T) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{fakeDockerHost(t, "")},
		session: session,
	}, rt
}

// TestHandleModalAgregaRecusaPorRateLimitNumaJanela é o espelho do teste
// acima para o outro ponto de recusa: o /exec (ops.go), que tem seu próprio
// site de auditoria e sua própria checagem de allow-list antes do limiter.
func TestHandleModalAgregaRecusaPorRateLimitNumaJanela(t *testing.T) {
	b, rt := newRefusalExecWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.refusals.after = canalQueNuncaFecha
	b.limiter = newRateLimiter(0, 0) // balde vazio: toda ação é recusada

	for n := 0; n < 5; n++ {
		b.handleModal(refusalModalInteraction("echo oi"))
	}

	if strings.Contains(string(rt.all()), "rate-limit") {
		t.Fatal("auditoria publicou antes da janela fechar")
	}

	b.flushRefusals()
	b.auditWG.Wait()

	sent := string(rt.all())
	if n := strings.Count(sent, "rate-limit"); n != 1 {
		t.Fatalf("esperava exatamente 1 embed agregado de rate-limit, vieram %d; corpo: %q", n, sent)
	}
	if !strings.Contains(sent, "5 ação(ões) recusada(s) por rate limit em 16s") {
		t.Fatalf("embed agregado não menciona a contagem 5: %q", sent)
	}
}

// TestFlushRefusalsIdempotenteSemJanelaAberta prova o motivo de flushRefusals
// poder ser chamado tanto pelo timer da janela quanto pelo Stop() (bot.go)
// sem risco de embed duplicado: com n == 0, é no-op.
func TestFlushRefusalsIdempotenteSemJanelaAberta(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"

	b.flushRefusals()
	b.auditWG.Wait()

	if len(rt.all()) != 0 {
		t.Fatalf("flushRefusals sem janela aberta não devia publicar nada: %q", rt.all())
	}
}

// TestHandleConfirmAgregaRecusaPorRateLimit é o espelho para o caminho
// pós-confirmação (stop/restart) — portado da proposta de 25/08/2026
// (1-para-1, descartada), que cobria este call site e a agregada não.
func TestHandleConfirmAgregaRecusaPorRateLimit(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.refusals.after = canalQueNuncaFecha
	b.limiter = newRateLimiter(0, 0) // balde vazio: toda ação é recusada
	token := b.confirms.add("stop", "main", "web", actionInteraction("act:stop:main:web").Interaction)

	b.handleConfirm(actionInteraction("cfm:ok:"+token), "cfm:ok:"+token)

	b.flushRefusals()
	b.auditWG.Wait()

	sent := string(rt.all())
	if n := strings.Count(sent, "rate-limit"); n != 1 {
		t.Fatalf("esperava exatamente 1 embed agregado de rate-limit, vieram %d; corpo: %q", n, sent)
	}
	if !strings.Contains(sent, "1 ação(ões) recusada(s) por rate limit em 16s") {
		t.Fatalf("embed agregado não menciona a contagem 1: %q", sent)
	}
}

// TestHandleActionSemRateLimitNaoAuditaComoRecusa é a contraprova positiva
// (portada da proposta de 25/08/2026): com o limiter liberando (o caso
// normal), a ação audita como sucesso e NENHUMA janela de recusa abre — sem
// ela, um auditRefusal chamado incondicionalmente passaria pelos testes de
// recusa acima.
func TestHandleActionSemRateLimitNaoAuditaComoRecusa(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.limiter = newRateLimiter(8, 0.5) // Allow() sempre true na 1ª chamada

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")

	b.flushRefusals() // se alguma janela tivesse aberto, o embed agregado sairia aqui
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "iniciado") {
		t.Fatalf("auditoria do sucesso não registrou 'iniciado': %q", sent)
	}
	if strings.Contains(sent, "rate-limit") {
		t.Fatal("ação permitida pelo limiter foi auditada como recusa por rate limit")
	}
}
