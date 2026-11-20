package discord

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Fiação do fail-closed do /exec (decisão D1, ciclo 20/11/2026): quando o
// defer de handleModal (InteractionRespond) falha, o comando NÃO pode chegar
// ao daemon. O teste conta as requisições ao endpoint de exec do stub da API
// do Docker — a prova é "o daemon nunca viu o exec", não "o texto da
// auditoria diz que não rodou".

// execContadoHost sobe um stub da API do Docker que conta toda requisição cujo
// caminho contém "/exec" (create em /containers/{id}/exec e start/inspect em
// /exec/{id}/...). O create responde 500: aqui só interessa SE o daemon foi
// alcançado, não o resultado do comando.
func execContadoHost(t *testing.T) (*dockerx.Client, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/exec") {
			hits.Add(1)
			http.Error(w, `{"message":"stub"}`, http.StatusInternalServerError)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"deadbeef","Config":{"Tty":false},"State":{"Status":"running"}}`))
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
	return h, &hits
}

func newExecDeferBot(t *testing.T, rt *recordingTransport) (*Bot, *atomic.Int32) {
	t.Helper()
	session, err := discordgo.New("Bot token-de-teste")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = &http.Client{Transport: rt}
	h, hits := execContadoHost(t)
	return &Bot{
		cfg:     &config.Config{AuditChannelID: "canal-auditoria"},
		hosts:   []*dockerx.Client{h},
		session: session,
		limiter: newRateLimiter(100, 1),
	}, hits
}

// TestExecDeferFalhoNaoExecuta: callback recusado (403) → o daemon não recebe
// nenhuma requisição de exec, a auditoria registra "⛔ não executado" e o log
// diz por quê.
func TestExecDeferFalhoNaoExecuta(t *testing.T) {
	rt := &recordingTransport{failCallback: true}
	b, hits := newExecDeferBot(t, rt)

	saida := capturaLog(func() {
		b.handleModal(execModalInteraction("rm -rf /tmp/x"))
		b.auditWG.Wait()
	})

	if n := hits.Load(); n != 0 {
		t.Fatalf("defer falhou mas o daemon recebeu %d requisição(ões) de exec — o comando rodou às cegas", n)
	}
	res := auditResultado(t, rt)
	if !strings.HasPrefix(strings.TrimPrefix(res, "```\n"), "⛔ não executado") {
		t.Fatalf("auditoria deveria começar com \"⛔ não executado\"; Resultado = %q", res)
	}
	if !strings.Contains(saida, `/exec "web": defer falhou, comando NAO executado`) {
		t.Fatalf("log não registrou a falha do defer; log = %q", saida)
	}
}

// TestExecDeferFalhoRedigeToken: falha de TRANSPORTE no callback (o *url.Error
// imprime a URL, que carrega o token) → nem o log nem a auditoria podem conter
// o token; e o comando também não roda.
func TestExecDeferFalhoRedigeToken(t *testing.T) {
	rt := &recordingTransport{netErrCallback: true}
	b, hits := newExecDeferBot(t, rt)

	const segredo = "TOKEN-SECRETO-DA-INTERACAO-0123456789"
	i := execModalInteraction("id")
	i.Interaction.Token = segredo
	saida := capturaLog(func() {
		b.handleModal(i)
		b.auditWG.Wait()
	})

	if n := hits.Load(); n != 0 {
		t.Fatalf("defer falhou (transporte) mas o daemon recebeu %d requisição(ões) de exec", n)
	}
	if strings.Contains(saida, segredo) {
		t.Fatalf("token da interação vazou no log: %q", saida)
	}
	res := auditResultado(t, rt)
	if strings.Contains(res, segredo) {
		t.Fatalf("token da interação vazou na auditoria: %q", res)
	}
	// Contraprova positiva no MESMO alvo: a URL do callback chegou ao texto
	// (senão a ausência do token não provaria nada) e foi redigida.
	if !strings.Contains(saida, "<token-redigido>") || !strings.Contains(res, "<token-redigido>") {
		t.Fatalf("esperava a URL do callback com <token-redigido> no log e na auditoria; log=%q res=%q", saida, res)
	}
}

// TestExecDeferOkExecuta é o controle positivo do contador: com o callback
// vivo, o exec CHEGA ao daemon. Sem isto, "0 requisições" nos testes acima
// poderia ser só um stub que nunca é alcançado.
func TestExecDeferOkExecuta(t *testing.T) {
	rt := &recordingTransport{}
	b, hits := newExecDeferBot(t, rt)

	_ = capturaLog(func() {
		b.handleModal(execModalInteraction("id"))
		b.auditWG.Wait()
	})

	if n := hits.Load(); n == 0 {
		t.Fatalf("com o defer OK o exec deveria alcançar o daemon; 0 requisições ao stub")
	}
	// audit() cerca o Resultado em bloco de código: o prefixo vem depois da cerca.
	if res := auditResultado(t, rt); strings.HasPrefix(strings.TrimPrefix(res, "```\n"), "⛔ não executado") {
		t.Fatalf("defer OK não pode auditar \"não executado\"; Resultado = %q", res)
	}
}
