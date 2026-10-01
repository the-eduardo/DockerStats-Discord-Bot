package discord

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do orçamento de 2000 chars do /exec (achado do
// enxame, 28/09/2026): header = "`$ "+truncate(cmd,120)+"` em **"+name+"**:\n"
// (134+N runes) somado ao codeBlock truncado (1870 runes) estoura 2004+N,
// e os 3 editResponse de handleModal descartavam o erro do PATCH enquanto a
// auditoria gravava "✅ executado" com nada publicado. Reusa
// fakeExecHostSaida/tokenDaInteracao/auditResultado/efemeroConteudo/
// recordingTransport dos arquivos irmãos — não redefinir nenhum deles.

// execModalInteracaoComAlvo é execModalInteraction com hostKey/name/token
// parametrizados, necessário para T1 (name longo, estourando o header) e T2
// (token distintivo, provando não-vazamento).
func execModalInteracaoComAlvo(cmd, hostKey, name, tok string) *discordgo.InteractionCreate {
	data := discordgo.ModalSubmitInteractionData{
		CustomID: "exec:" + target(hostKey, name),
		Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				&discordgo.TextInput{CustomID: "cmd", Value: cmd},
			}},
		},
	}
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		ID:   "1", AppID: "2", Token: tok,
		Data: data,
	}}
}

// TestHandleModalRespostaCabeNoTetoDe2000 prova o teto (T1): saída grande +
// cmd de 120+ runes + nome de container longo não pode produzir um content
// acima de maxMessage — e o cabeçalho (comando/container) tem que
// permanecer identificável, porque é ele que evita o operador repetir um
// exec já executado.
func TestHandleModalRespostaCabeNoTetoDe2000(t *testing.T) {
	cmd := strings.Repeat("x", 130) // > 120: o header usa truncate(cmd,120)
	name := strings.Repeat("y", 30)
	payload := strings.Repeat("linha de saida do exec bem grande para estourar o teto de 2000 chars do discord\n", 60) // > 4 KiB

	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{fakeExecHostSaida(t, 0, false, payload)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}

	b.handleModal(execModalInteracaoComAlvo(cmd, "main", name, "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if n := len([]rune(content)); n > maxMessage {
		t.Fatalf("resposta efêmera do /exec estourou o teto do Discord: %d runes (max %d); content: %.200q", n, maxMessage, content)
	}
	if !strings.Contains(content, truncate(cmd, 120)) {
		t.Fatalf("cabeçalho não preservou o comando (truncado em 120 runes): %.200q", content)
	}
	if !strings.Contains(content, name) {
		t.Fatalf("cabeçalho não preservou o nome do container: %.200q", content)
	}
}

// TestHandleModalAuditaComoNaoPublicadoQuandoEditFalha prova T2: quando o
// PATCH de @original falha (recordingTransport.failEdit), a auditoria não
// pode mentir "✅ executado" (regra de cor invertida do audit(): verde só com
// prefixo ✅), e o token da interação — que vaza em *url.Error de falha de
// transporte — não pode aparecer em nenhum corpo enviado ao Discord nem no
// log.
func TestHandleModalAuditaComoNaoPublicadoQuandoEditFalha(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	rt := &recordingTransport{failEdit: true}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	b := &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{fakeExecHost(t, 0, false)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}

	b.handleModal(execModalInteracaoComAlvo("echo oi", "main", "web", tokenDaInteracao))
	b.auditWG.Wait()

	resultado := auditResultado(t, rt)
	if strings.HasPrefix(resultado, "✅") {
		t.Fatalf("auditoria marcou sucesso (✅) apesar do PATCH de @original ter falhado: %q", resultado)
	}
	if !strings.Contains(resultado, "não publicada") {
		t.Fatalf("auditoria não registrou que a saída não foi publicada: %q", resultado)
	}

	corpo := string(rt.all())
	if strings.Contains(corpo, tokenDaInteracao) {
		t.Fatalf("token da interação vazou num corpo enviado ao Discord: %q", corpo)
	}
	if strings.Contains(buf.String(), tokenDaInteracao) {
		t.Fatalf("token da interação vazou no log: %q", buf.String())
	}
}

// novoBotExecOrcamento monta o Bot do /exec com o stub de Docker parametrizado
// (exit code, ainda-rodando, payload) e o transporte gravador dado.
func novoBotExecOrcamento(t *testing.T, rt *recordingTransport, exitCode int, running bool, payload string) *Bot {
	t.Helper()
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{fakeExecHostSaida(t, exitCode, running, payload)},
		session: session,
		limiter: newRateLimiter(100, 1),
	}
}

// TestHandleModalRespostaCabeNoTetoEmTodosOsExitCodes prova que os TRÊS
// ramos do switch de handleModal (exit 0, exit > 0, exit desconhecido) passam
// por execBody. Drenagem 30/09/2026: o teste original só exercitava exit 0, e
// reverter os ramos exit>0 e exit<0 para o codeBlock antigo (que presume
// Content vazio e estoura 2000 com o cabeçalho) deixava a suíte verde.
func TestHandleModalRespostaCabeNoTetoEmTodosOsExitCodes(t *testing.T) {
	cmd := strings.Repeat("x", 130)
	name := strings.Repeat("y", 30)
	payload := strings.Repeat("linha de saida do exec bem grande para estourar o teto de 2000 chars do discord\n", 60)
	casos := []struct {
		nome     string
		exitCode int
		running  bool
	}{
		{"exit>0", 3, false},
		{"exit desconhecido (ainda rodando)", 0, true},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rt := &recordingTransport{}
			b := novoBotExecOrcamento(t, rt, c.exitCode, c.running, payload)
			b.handleModal(execModalInteracaoComAlvo(cmd, "main", name, "tok"))
			b.auditWG.Wait()

			content := efemeroConteudo(t, rt)
			if n := len([]rune(content)); n > maxMessage {
				t.Fatalf("%s: resposta efêmera estourou o teto do Discord: %d runes (max %d)", c.nome, n, maxMessage)
			}
			if !strings.Contains(content, name) {
				t.Fatalf("%s: cabeçalho não preservou o nome do container: %.200q", c.nome, content)
			}
		})
	}
}

// TestHandleModalCabecalhoCurtoMantemTetoDoBloco prova o clamp de execBody
// (`budget > maxBlock`): com cabeçalho curto sobra mais de maxBlock de
// orçamento, e o bloco tem que continuar truncado em maxBlock (comportamento
// anterior do /exec), não crescer até o teto da mensagem.
func TestHandleModalCabecalhoCurtoMantemTetoDoBloco(t *testing.T) {
	payload := strings.Repeat("L", 5000)
	rt := &recordingTransport{}
	b := novoBotExecOrcamento(t, rt, 0, false, payload)
	b.handleModal(execModalInteracaoComAlvo("ls", "main", "web", "tok"))
	b.auditWG.Wait()

	content := efemeroConteudo(t, rt)
	if got := strings.Count(content, "L"); got != maxBlock {
		t.Fatalf("bloco truncado em %d bytes de saída, esperado maxBlock=%d; content: %.120q", got, maxBlock, content)
	}
}

// TestCodeBlockTruncaNoMaxBlock fixa o teto do codeBlock (usado por /logs e
// pelos ramos que não passam por execBody): refactor para codeBlockMax não
// pode mudar o ponto de corte.
func TestCodeBlockTruncaNoMaxBlock(t *testing.T) {
	got := codeBlock(strings.Repeat("L", 5000))
	if n := strings.Count(got, "L"); n != maxBlock {
		t.Fatalf("codeBlock manteve %d bytes de saída, esperado maxBlock=%d", n, maxBlock)
	}
	if !strings.Contains(got, "…(truncado)") {
		t.Fatalf("codeBlock não marcou o truncamento: %.60q", got)
	}
}

// TestHandleModalEditFalhaAuditaSemSucessoVerde prova a regra de cor do
// audit(): o aviso "saída não publicada" tem que ser PREFIXO do Resultado —
// verde só com prefixo ✅. O campo Resultado chega embrulhado em cerca
// (```\n...\n```), então a checagem tem que tirar a cerca antes; a asserção
// original `HasPrefix(resultado, "✅")` olhava o texto COM cerca e nunca podia
// ser verdadeira (vazia: sobrevivia à troca de prefixo por sufixo).
func TestHandleModalEditFalhaAuditaSemSucessoVerde(t *testing.T) {
	rt := &recordingTransport{failEdit: true}
	b := novoBotExecOrcamento(t, rt, 0, false, "saida\n")
	b.handleModal(execModalInteracaoComAlvo("echo oi", "main", "web", "tok"))
	b.auditWG.Wait()

	resultado := strings.TrimPrefix(auditResultado(t, rt), "```\n")
	if !strings.HasPrefix(resultado, "⚠️ saída não publicada") {
		t.Fatalf("Resultado devia COMEÇAR com o aviso (cor não-verde): %q", resultado)
	}
}

// TestHandleModalEditFalhaDeTransporteNaoVazaToken prova o errSafe nas duas
// saídas novas (log e Resultado da auditoria). O 403 do failEdit é *RESTError
// e não carrega o token; só a falha de TRANSPORTE (netErrEdit) devolve um
// *url.Error com a URL de @original, que contém o token da interação. Sem
// este caso, trocar errSafe(i, err) por err.Error() não quebrava teste algum.
func TestHandleModalEditFalhaDeTransporteNaoVazaToken(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	rt := &recordingTransport{netErrEdit: true}
	b := novoBotExecOrcamento(t, rt, 0, false, "saida\n")
	b.handleModal(execModalInteracaoComAlvo("echo oi", "main", "web", tokenDaInteracao))
	b.auditWG.Wait()

	resultado := auditResultado(t, rt)
	if !strings.Contains(resultado, "não publicada") {
		t.Fatalf("auditoria não registrou a saída não publicada: %q", resultado)
	}
	if strings.Contains(resultado, tokenDaInteracao) {
		t.Fatalf("token vazou no Resultado da auditoria: %q", resultado)
	}
	logado := buf.String()
	if !strings.Contains(logado, "/exec") {
		t.Fatalf("falha de publicação não foi registrada no log: %q", logado)
	}
	if strings.Contains(logado, tokenDaInteracao) {
		t.Fatalf("token vazou no log: %q", logado)
	}
}
