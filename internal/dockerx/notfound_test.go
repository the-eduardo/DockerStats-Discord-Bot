package dockerx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ensureExists (e por extensão Start/Stop/Restart/Pause/Unpause/Exec/State/Logs)
// colapsava QUALQUER erro de ContainerInspect em ErrNotFound — SSH caído,
// socket-proxy fora, timeout de contexto viravam "container não encontrado",
// um diagnóstico ativamente errado. Este arquivo prova que o classificador
// separa 404 (container de fato ausente) de falha de transporte (5xx, aqui).

// notFoundStub sobe um stub mínimo da API do Docker cujo /containers/<nome>/json
// devolve o status dado, simulando ou um 404 real do daemon ou uma falha de
// transporte (5xx: proxy fora, host caído).
func notFoundStub(t *testing.T, status int) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if status == http.StatusNotFound {
				_, _ = w.Write([]byte(`{"message":"No such container: web"}`))
			} else {
				_, _ = w.Write([]byte(`{"message":"proxy fora do ar"}`))
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	c, err := NewLocal("main", "Main")
	if err != nil {
		t.Fatalf("NewLocal contra o stub: %v", err)
	}
	return c
}

func TestEnsureExistsClassificaTransporteComoErroNaoComoNotFound(t *testing.T) {
	c := notFoundStub(t, http.StatusInternalServerError)
	err := c.ensureExists(context.Background(), "web")
	if err == nil {
		t.Fatal("esperava erro (proxy fora), veio nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("falha de transporte (500) foi classificada como ErrNotFound: %v", err)
	}
}

func TestEnsureExistsClassifica404ComoNotFound(t *testing.T) {
	c := notFoundStub(t, http.StatusNotFound)
	err := c.ensureExists(context.Background(), "web")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 real não foi classificado como ErrNotFound: %v", err)
	}
}

// TestStartStopRestartPauseUnpauseExecPropagamFalhaDeTransporte é o teste de
// FIAÇÃO: prova que os 6 call sites que usavam exists()/ContainerInspect direto
// REALMENTE chamam o classificador novo, e não engolem o 500 em ErrNotFound
// por conta própria. Sem isso, ensureExists podia estar certo e um call site
// esquecido continuar chamando o exists() antigo (ou reimplementando o bug).
func TestStartStopRestartPauseUnpauseExecPropagamFalhaDeTransporte(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		nome string
		call func(c *Client) error
	}{
		{"Start", func(c *Client) error { return c.Start(ctx, "web") }},
		{"Stop", func(c *Client) error { return c.Stop(ctx, "web", 5) }},
		{"Restart", func(c *Client) error { return c.Restart(ctx, "web", 5) }},
		{"Pause", func(c *Client) error { return c.Pause(ctx, "web") }},
		{"Unpause", func(c *Client) error { return c.Unpause(ctx, "web") }},
		{"Exec", func(c *Client) error { _, err := c.Exec(ctx, "web", "echo oi"); return err }},
	}

	for _, tc := range cases {
		t.Run(tc.nome, func(t *testing.T) {
			c := notFoundStub(t, http.StatusInternalServerError)
			err := tc.call(c)
			if err == nil {
				t.Fatal("esperava erro (proxy fora), veio nil")
			}
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("%s: falha de transporte (500) virou ErrNotFound (mentira: container existe, host que está fora)", tc.nome)
			}
		})
	}
}

// TestStateELogsClassificam404VsTransporte prova a mesma classificação nos
// dois métodos que faziam ContainerInspect por conta própria (sem passar por
// ensureExists): State() e Logs().
func TestStateELogsClassificam404VsTransporte(t *testing.T) {
	ctx := context.Background()

	t.Run("State com 500 não é ErrNotFound", func(t *testing.T) {
		c := notFoundStub(t, http.StatusInternalServerError)
		_, err := c.State(ctx, "web")
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("State com falha de transporte devia dar erro != ErrNotFound, veio: %v", err)
		}
	})
	t.Run("State com 404 é ErrNotFound", func(t *testing.T) {
		c := notFoundStub(t, http.StatusNotFound)
		_, err := c.State(ctx, "web")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("State com 404 devia dar ErrNotFound, veio: %v", err)
		}
	})
	t.Run("Logs com 500 não é ErrNotFound", func(t *testing.T) {
		c := notFoundStub(t, http.StatusInternalServerError)
		_, err := c.Logs(ctx, "web", 0)
		if err == nil || errors.Is(err, ErrNotFound) {
			t.Fatalf("Logs com falha de transporte devia dar erro != ErrNotFound, veio: %v", err)
		}
	})
	t.Run("Logs com 404 é ErrNotFound", func(t *testing.T) {
		c := notFoundStub(t, http.StatusNotFound)
		_, err := c.Logs(ctx, "web", 0)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("Logs com 404 devia dar ErrNotFound, veio: %v", err)
		}
	})
}
