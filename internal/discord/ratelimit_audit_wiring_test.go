package discord

import (
	"fmt"
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
	// Recusa tem que chegar com cor de alerta (colorBusy), nao verde de
	// sucesso — o switch de audit.go decide pela ⚠️ do result (QA, 29/08).
	if !strings.Contains(sent, fmt.Sprintf("\"color\":%d", colorBusy)) {
		t.Fatalf("recusa agregada saiu com cor de sucesso: %q", sent)
	}
}

// refusalModalInteraction monta a interação de submissão do modal de /exec,
// no formato que handleModal espera (CustomID "exec:<hostKey>:<container>",
// valor do campo "cmd" dentro de um ActionsRow/TextInput). O teste nunca
// alcança host.Exec (a recusa por rate limit sai antes), então dispensa o
// stub de hijack do caminho feliz do /exec (coberto so na branch de 26/08,
// exec_audit_wiring_test.go — nao existe nesta branch isolada).
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

// TestStopPublicaJanelaDeRecusaAberta prova a FIAÇÃO do Stop() com o
// flushRefusals: janela aberta no momento do shutdown vira o embed agregado
// antes de a sessão fechar. A mutação "remover o flushRefusals() do Stop"
// sobreviveu à suíte inteira em 29/08/2026 — este é o guardião que faltava.
func TestStopPublicaJanelaDeRecusaAberta(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.refusals.after = canalQueNuncaFecha
	b.limiter = newRateLimiter(0, 0) // balde vazio: a ação abre a janela

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	if strings.Contains(string(rt.all()), "rate-limit") {
		t.Fatal("auditoria publicou antes do Stop")
	}

	b.Stop()

	sent := string(rt.all())
	if n := strings.Count(sent, "rate-limit"); n != 1 {
		t.Fatalf("Stop() com janela aberta devia publicar exatamente 1 embed agregado, vieram %d; corpo: %q", n, sent)
	}
}

// TestHandleModalSemRateLimitNaoAbreJanelaDeRecusa é a contraprova positiva
// do SEGUNDO call site (handleModal/exec): um auditRefusal incondicional em
// ops.go sobrevivia à suíte inteira (mutação medida pelo QA no comitê de
// 29/08/2026) porque a contraprova existente cobria só o handleAction.
func TestHandleModalSemRateLimitNaoAbreJanelaDeRecusa(t *testing.T) {
	b, rt := newRefusalExecWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.limiter = newRateLimiter(8, 0.5) // Allow() true na 1ª chamada

	b.handleModal(refusalModalInteraction("echo oi"))

	b.flushRefusals() // se alguma janela tivesse aberto, o agregado sairia aqui
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "Erro no exec") {
		t.Fatalf("caminho permitido não chegou ao exec (controle positivo): %q", sent)
	}
	if strings.Contains(sent, "rate-limit") {
		t.Fatal("/exec permitido pelo limiter abriu janela de recusa por rate limit")
	}
}

// TestFlushRefusalsComVariosAlvosNaoCarimbaOPrimeiro: com N > 1 recusas de
// alvos diferentes na mesma janela, o embed agregado não pode atribuir tudo
// ao host/container da PRIMEIRA (QA, 29/08/2026).
func TestFlushRefusalsComVariosAlvosNaoCarimbaOPrimeiro(t *testing.T) {
	b, rt := newActionWiringBot(t)
	b.cfg.AuditChannelID = "999"
	b.refusals.after = canalQueNuncaFecha
	b.limiter = newRateLimiter(0, 0)

	b.handleAction(actionInteraction("act:start:main:web"), "act:start:main:web")
	b.handleAction(actionInteraction("act:start:main:db"), "act:start:main:db")

	b.flushRefusals()
	b.auditWG.Wait()

	sent := string(rt.all())
	if n := strings.Count(sent, "rate-limit"); n != 1 {
		t.Fatalf("esperava 1 embed agregado, vieram %d: %q", n, sent)
	}
	if !strings.Contains(sent, "2 ação(ões) recusada(s)") {
		t.Fatalf("agregado não menciona a contagem 2: %q", sent)
	}
	// Controle positivo: os DOIS campos tem que render o vazio ("—"). Sem ele
	// as assercoes negativas abaixo sao desarmadas por formatacao (tirar as
	// crases do Container em audit.go) e o lado do HOST fica sem guardiao
	// (mutacao M-A do relampago do QA sobreviveu sem isto).
	if !strings.Contains(sent, "\"name\":\"Host\",\"value\":\"—\"") {
		t.Fatalf("Host do agregado nao foi zerado (deveria render \"—\"): %q", sent)
	}
	if !strings.Contains(sent, "\"name\":\"Container\",\"value\":\"`—`\"") {
		t.Fatalf("Container do agregado nao foi zerado (deveria render `—`): %q", sent)
	}
	// item 4 do relampago: o actor zerado vira o Author "—" do embed.
	if !strings.Contains(sent, "\"author\":{\"name\":\"—\"}") {
		t.Fatalf("Author do agregado nao foi zerado (deveria render \"—\"): %q", sent)
	}
	for _, alvo := range []string{"web", "db"} {
		// O campo Container do embed rende `alvo` entre backticks (audit.go).
		if strings.Contains(sent, "`"+alvo+"`") {
			t.Fatalf("agregado de alvos mistos carimbou o alvo %q: %q", alvo, sent)
		}
	}
}
