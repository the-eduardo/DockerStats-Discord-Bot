package discord

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO, não as funções puras: tailBytes tem testes
// próprios, mas remover a chamada `out = tailBytes(out, maxAttach)` de dentro
// do cmdLogs deixava a suíte inteira verde (verificado por mutação em
// 15/08/2026) — e produção voltaria a estourar o teto de upload. O padrão já
// repetiu três vezes no acervo do triador: função pura bem testada, call site
// sem teste nenhum.

// recordingTransport intercepta TODA chamada HTTP do discordgo: nada sai para
// a rede, e cada corpo enviado fica guardado para inspeção.
type recordingTransport struct {
	mu     sync.Mutex
	bodies [][]byte
	// failEdit força 403 no PATCH de @original (InteractionResponseEdit),
	// simulando o Discord recusando a edição — POST (callback e embed de
	// auditoria) segue 200. 403 e não 5xx/429 de propósito: discordgo v0.29.0
	// retenta 5xx e dorme no 429; 403 volta na hora como *RESTError.
	failEdit bool
	// netErrEdit força uma falha de TRANSPORTE no PATCH (connection reset,
	// timeout): o http.Client embrulha num *url.Error que imprime a URL
	// inteira, e a URL de @original carrega o token da interação. É o caminho
	// que VAZA credencial — o 403 do failEdit vem como *RESTError e não vaza.
	netErrEdit bool
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body.Close()
	}
	rt.mu.Lock()
	rt.bodies = append(rt.bodies, body)
	rt.mu.Unlock()
	if rt.netErrEdit && req.Method == http.MethodPatch {
		return nil, errors.New("read: connection reset by peer")
	}
	if rt.failEdit && req.Method == http.MethodPatch {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"message":"Missing Access","code":50001}`)),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("{}")),
		Request:    req,
	}, nil
}

func (rt *recordingTransport) all() []byte {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	var out []byte
	for _, b := range rt.bodies {
		out = append(out, b...)
	}
	return out
}

// bodiesCopy devolve os corpos INDIVIDUAIS (all() concatena tudo e impede
// decodificar o JSON de um corpo isolado).
func (rt *recordingTransport) bodiesCopy() [][]byte {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	out := make([][]byte, len(rt.bodies))
	copy(out, rt.bodies)
	return out
}

// auditResultado decodifica o embed de auditoria e devolve o campo
// "Resultado". Comparar o campo direto é mais robusto que strings.Contains
// sobre o dump bruto de TODOS os corpos HTTP: ali um texto pode casar com a
// resposta enviada ao usuário em vez do registro de auditoria, e a asserção
// passa (ou falha) pelo motivo errado. Achado do QA na drenagem de
// 12/09/2026.
func auditResultado(t *testing.T, rt *recordingTransport) string {
	t.Helper()
	for _, body := range rt.bodiesCopy() {
		var payload struct {
			Embeds []struct {
				Fields []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"fields"`
			} `json:"embeds"`
		}
		if json.Unmarshal(body, &payload) != nil {
			continue
		}
		for _, e := range payload.Embeds {
			for _, f := range e.Fields {
				if f.Name == "Resultado" {
					return f.Value
				}
			}
		}
	}
	t.Fatalf("nenhum embed de auditoria com campo Resultado nos corpos gravados: %q", rt.all())
	return ""
}

// fakeDockerHost sobe um stub mínimo da API do Docker (ping, inspect com
// Tty=true para o log vir cru, logs com o payload dado) e devolve um
// *dockerx.Client apontado para ele via DOCKER_HOST.
func fakeDockerHost(t *testing.T, logPayload string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "deadbeef",
				"Config": map[string]any{"Tty": true},
				"State":  map[string]any{"Status": "running"},
			})
		case strings.HasSuffix(r.URL.Path, "/logs"):
			_, _ = io.WriteString(w, logPayload)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal("main", "Main")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

func newWiringBot(t *testing.T, logPayload string) (*Bot, *recordingTransport) {
	t.Helper()
	rt := &recordingTransport{}
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	return &Bot{
		cfg:     &config.Config{},
		hosts:   []*dockerx.Client{fakeDockerHost(t, logPayload)},
		session: session,
	}, rt
}

// tokenDaInteracao é longo e distintivo de propósito: o "tok" de 3 letras que
// esta fixture usava antes casa por acidente em qualquer strings.Contains
// (aparece dentro de "token-de-teste", por exemplo), o que tornaria a
// asserção de não-vazamento um falso-verde permanente.
const tokenDaInteracao = "IntTok-9f3c8b2e17d45a6c0eNAODEVEVAZAR"

func logsInteraction() *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand,
		ID:   "1", AppID: "2", Token: tokenDaInteracao,
		Data: discordgo.ApplicationCommandInteractionData{
			Name: "logs",
			Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "container", Type: discordgo.ApplicationCommandOptionString, Value: "web"},
			},
		},
	}}
}

func TestCmdLogsAppliesAttachmentCapBeforeUpload(t *testing.T) {
	// Log maior que maxAttach: o anexo enviado TEM que sair truncado, com o
	// marcador do tailBytes no começo do arquivo.
	line := strings.Repeat("linha de log bem comprida para encher o anexo ", 2) + "\n"
	payload := strings.Repeat(line, (maxAttach/len(line))+2_000) // ~7.2 MiB + folga

	b, rt := newWiringBot(t, payload)
	b.cmdLogs(logsInteraction())

	sent := rt.all()
	if len(sent) == 0 {
		t.Fatal("nenhuma chamada chegou ao Discord fake")
	}
	if !strings.Contains(string(sent), "…(truncado") {
		t.Fatal("anexo subiu SEM o marcador de truncamento: cmdLogs não passou o log por tailBytes antes do upload")
	}
	// Corpo total (multipart + json do defer) tem que ficar perto do teto, não
	// no tamanho original do log.
	if len(sent) > maxAttach+4096 {
		t.Fatalf("upload com %d bytes, quer <= %d: o teto não foi aplicado", len(sent), maxAttach+4096)
	}
}

func TestCmdLogsSendsSmallLogInlineWithoutTruncation(t *testing.T) {
	// Contraprova: log pequeno vai inline, sem marcador — garante que o teste
	// acima detecta o teto, não um truncamento incondicional.
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cmdLogs(logsInteraction())

	sent := string(rt.all())
	if strings.Contains(sent, "…(truncado") {
		t.Fatal("log pequeno saiu truncado")
	}
	if !strings.Contains(sent, "só uma linha") {
		t.Fatal("log pequeno não chegou na resposta")
	}
}

// README.md promete "every action is logged (who, what, host, container,
// exec command, result)". /logs e o botão Logs eram os únicos handlers da
// superfície perigosa sem nenhuma chamada a b.audit — nenhum dos dois testes
// acima cobre isso, porque nenhum configura AuditChannelID. Estes três testes
// fecham essa lacuna testando a FIAÇÃO real (cmdLogs/showLogsEphemeral ->
// b.audit), não uma condição isolada.

func TestCmdLogsAuditaLeitura(t *testing.T) {
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cfg.AuditChannelID = "999"

	b.cmdLogs(logsInteraction())
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "\"name\":\"Ação\",\"value\":\"`logs`\"") {
		t.Fatalf("auditoria de /logs nao registrou a acao 'logs': %q", sent)
	}
	if !strings.Contains(sent, "`web`") {
		t.Fatalf("auditoria de /logs nao registrou o container: %q", sent)
	}
	if !strings.Contains(sent, "30 min") {
		t.Fatalf("auditoria de /logs nao registrou a janela de minutos: %q", sent)
	}
	if !strings.Contains(sent, "publicado no canal") {
		t.Fatalf("auditoria de /logs nao distinguiu 'publicado no canal': %q", sent)
	}
	if n := strings.Count(sent, "`logs`"); n != 1 {
		t.Fatalf("esperava exatamente 1 embed de auditoria para /logs, indicio de %d: %q", n, sent)
	}
}

func TestBotaoLogsAuditaLeituraEfemera(t *testing.T) {
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cfg.AuditChannelID = "999"

	// O ramo "logs" de handleAction chama showLogsEphemeral diretamente, sem
	// tocar o dashboard — newWiringBot (sem b.dashboard) basta.
	b.handleAction(actionInteraction("act:logs:main:web"), "act:logs:main:web")
	b.auditWG.Wait()

	sent := string(rt.all())
	if !strings.Contains(sent, "\"name\":\"Ação\",\"value\":\"`logs`\"") {
		t.Fatalf("auditoria do botao Logs nao registrou a acao 'logs': %q", sent)
	}
	if !strings.Contains(sent, "efêmero") {
		t.Fatalf("auditoria do botao Logs nao distinguiu 'efêmero': %q", sent)
	}
}

func TestCmdLogsSemCanalDeAuditoriaNaoPublica(t *testing.T) {
	// Contraprova: sem AUDIT_CHANNEL_ID configurado, b.audit() e' no-op — os
	// dois testes acima so provam algo porque este continua passando.
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cfg.AuditChannelID = ""

	b.cmdLogs(logsInteraction())
	b.auditWG.Wait()

	sent := string(rt.all())
	if strings.Contains(sent, "`logs`") {
		t.Fatalf("com AuditChannelID vazio, /logs nao deveria publicar auditoria: %q", sent)
	}
}

// editResponse (ops.go) descartava o erro do InteractionResponseEdit — o
// ramo inline de cmdLogs/showLogsEphemeral marcava a auditoria como
// "publicado"/"efêmero" mesmo quando o Discord recusou a edição (403, ex.
// canal sem permissão de Send Messages). Estes dois testes provam a FIAÇÃO
// do fix: falha real de publicação tem que aparecer no Resultado, não sumir
// atrás de um ✅.

func TestCmdLogsNaoAuditaSucessoQuandoAPublicacaoFalha(t *testing.T) {
	// Dois tamanhos de payload, ambos no ramo INLINE (<= maxBlock). O caso
	// "medio" existe porque uma mutação SIZE-GATED sobreviveu ao caso curto
	// sozinho (achado do QA na drenagem de 12/09/2026): condicionar o
	// tratamento do erro a `len(out) <= 20` só reporta a falha para um payload
	// do tamanho exato do de teste (~14 bytes) e deixa o bug de pé para
	// qualquer log realista — com a suíte inteira verde.
	casos := []struct {
		nome    string
		payload string
	}{
		{"payload curto", "só uma linha\n"},
		{"payload medio", strings.Repeat("linha de log ", 50) + "\n"}, // ~650 B, < maxBlock
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			b, rt := newWiringBot(t, tc.payload)
			b.cfg.AuditChannelID = "999"
			rt.failEdit = true

			b.cmdLogs(logsInteraction())
			b.auditWG.Wait()

			resultado := auditResultado(t, rt)
			if strings.Contains(resultado, "publicado no canal") {
				t.Fatalf("auditoria afirmou publicação que o Discord recusou (403); Resultado=%q", resultado)
			}
			if !strings.Contains(resultado, "publicação falhou") {
				t.Fatalf("auditoria não registrou a falha de publicação; Resultado=%q", resultado)
			}
		})
	}
}

// TestCmdLogsNaoVazaTokenDaInteracaoNaFalhaDeRede: numa falha de TRANSPORTE
// (connection reset, timeout do Client de 20s do discordgo), o erro devolvido
// é um *url.Error que imprime a URL inteira — e a URL de @original é
// webhooks/<appID>/<TOKEN-DA-INTERAÇÃO>/messages/@original. Esse token é
// credencial real: os endpoints webhooks/<app>/<token> não pedem
// Authorization, então quem lê o texto do erro pode postar como o bot por ~15
// min. Sem errSafe, esse valor cai no canal de auditoria (durável, lido por
// mais gente que o console) e no stdout do container. Achado do painel AppSec
// na drenagem de 12/09/2026. Irmão do TestPushKumaNaoVazaOTokenNoLog.
func TestCmdLogsNaoVazaTokenDaInteracaoNaFalhaDeRede(t *testing.T) {
	var logBuf strings.Builder
	saida := log.Writer()
	flags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(saida); log.SetFlags(flags) })

	b, rt := newWiringBot(t, "só uma linha\n") // ramo INLINE
	b.cfg.AuditChannelID = "999"
	rt.netErrEdit = true

	b.cmdLogs(logsInteraction())
	b.auditWG.Wait()

	resultado := auditResultado(t, rt)
	// Controle positivo: a auditoria PRECISA ter registrado a falha — sem
	// isto, um Resultado vazio passaria a asserção de não-vazamento de graça.
	if !strings.Contains(resultado, "publicação falhou") {
		t.Fatalf("auditoria não registrou a falha de rede; Resultado=%q", resultado)
	}
	if !strings.Contains(resultado, "<token-redigido>") {
		t.Fatalf("a redação não atuou no Resultado (o erro do url.Error deveria trazer a URL com o token); Resultado=%q", resultado)
	}
	if strings.Contains(string(rt.all()), tokenDaInteracao) {
		t.Fatalf("o token da interação VAZOU no corpo enviado ao Discord: %q", resultado)
	}
	if strings.Contains(logBuf.String(), tokenDaInteracao) {
		t.Fatalf("o token da interação VAZOU no log do container: %q", logBuf.String())
	}
	// Controle positivo do log: a linha de diagnóstico não pode ter sumido —
	// redigir não pode virar silêncio.
	if !strings.Contains(logBuf.String(), "/logs") {
		t.Fatalf("a linha de log do erro de publicação sumiu: %q", logBuf.String())
	}
}

func TestBotaoLogsNaoAuditaSucessoQuandoAPublicacaoFalha(t *testing.T) {
	b, rt := newWiringBot(t, "só uma linha\n")
	b.cfg.AuditChannelID = "999"
	rt.failEdit = true

	b.handleAction(actionInteraction("act:logs:main:web"), "act:logs:main:web")
	b.auditWG.Wait()

	resultado := auditResultado(t, rt)
	if strings.Contains(resultado, "efêmero") {
		t.Fatalf("auditoria afirmou publicação efêmera que o Discord recusou (403); Resultado=%q", resultado)
	}
	if !strings.Contains(resultado, "publicação falhou") {
		t.Fatalf("auditoria não registrou a falha de publicação; Resultado=%q", resultado)
	}
}
