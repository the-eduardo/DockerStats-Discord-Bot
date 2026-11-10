package dockerx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
)

// execHijackStub sobe um stub mínimo da API do Docker que cobre o caminho
// inteiro do Exec(): cria a instância de exec, atende o start via hijack (o
// jeito real de obter a saída) e responde o inspect com o exitCode e o status
// dados. inspectStatus permite simular o inspect falhando (5xx) depois de o
// comando já ter rodado — cenário em que o exit code hoje simplesmente some.
func execHijackStub(t *testing.T, exitCode int, inspectStatus int, running bool) *Client {
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
				"Config": map[string]any{"Tty": false},
				"State":  map[string]any{"Status": "running"},
			})
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "exec1"})
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/start"):
			// Drena o corpo da requisição ANTES do hijack: se sobrar byte não lido
			// no socket quando fechamos a conexão, o kernel manda RST em vez de
			// FIN, e o client vê "connection reset by peer" de forma intermitente
			// (depende de o corpo já ter chegado ao buffer ou não).
			_, _ = io.Copy(io.Discard, r.Body)
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "servidor de teste não suporta hijack", http.StatusInternalServerError)
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			sw := stdcopy.NewStdWriter(buf, stdcopy.Stdout)
			_, _ = sw.Write([]byte("saida do comando\n"))
			_ = buf.Flush()
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			if inspectStatus != 0 && inspectStatus != http.StatusOK {
				w.WriteHeader(inspectStatus)
				_, _ = w.Write([]byte(`{"message":"inspect falhou"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ExitCode": exitCode, "Running": running})
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

// TestExecDevolveExitCodeDoInspect prova que Exec() propaga o exit code real
// (não apenas embutido no texto de saída) quando o comando termina != 0.
func TestExecDevolveExitCodeDoInspect(t *testing.T) {
	c := execHijackStub(t, 3, http.StatusOK, false)
	out, code, err := c.Exec(context.Background(), "web", "exit 3")
	if err != nil {
		t.Fatalf("Exec retornou erro inesperado: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, quer 3", code)
	}
	if !strings.Contains(out, "saida do comando") {
		t.Fatalf("saída do comando não chegou: %q", out)
	}
	if strings.Contains(out, "DESCONHECIDO") {
		t.Fatalf("exit code confirmado (!=0) não pode carregar o marcador DESCONHECIDO: %q", out)
	}
}

// TestExecExitCodeMenosUmQuandoInspectFalha prova que, quando o
// ContainerExecInspect falha (timeout, proxy fora), Exec() sinaliza que o
// exit code é DESCONHECIDO (-1) em vez de mentir que deu certo (0 implícito).
// A saída visível ao operador também carrega o aviso: sem ele, a resposta do
// /exec fica byte a byte igual à de um sucesso real.
func TestExecExitCodeMenosUmQuandoInspectFalha(t *testing.T) {
	c := execHijackStub(t, 0, http.StatusInternalServerError, false)
	out, code, err := c.Exec(context.Background(), "web", "echo oi")
	if err != nil {
		t.Fatalf("Exec retornou erro inesperado: %v", err)
	}
	if code != -1 {
		t.Fatalf("exit code = %d, quer -1 (inspect indisponível não pode virar 'sucesso')", code)
	}
	if !strings.Contains(out, "DESCONHECIDO") {
		t.Fatalf("saída não avisa que o exit code é desconhecido: %q", out)
	}
}

// execHijackStubPendurado simula um exec cujo stream NUNCA fecha por conta
// própria (ping, tail -f, top -b): escreve um frame parcial e trava até o
// cliente fechar a conexão (gone) ou o teste liberar (release). O release
// fecha num Cleanup registrado DEPOIS do srv.Close — como Cleanup roda em
// ordem LIFO, ele roda ANTES, e srv.Close nunca trava esperando o handler
// sob mutação (ex.: se o corte pelo ctx deixar de fechar a conexão).
func execHijackStubPendurado(t *testing.T) *Client {
	t.Helper()
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"Id":     "deadbeef",
				"Config": map[string]any{"Tty": false},
				"State":  map[string]any{"Status": "running"},
			})
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "exec1"})
		case strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/start"):
			_, _ = io.Copy(io.Discard, r.Body)
			hj, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "servidor de teste não suporta hijack", http.StatusInternalServerError)
				return
			}
			conn, buf, err := hj.Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			sw := stdcopy.NewStdWriter(buf, stdcopy.Stdout)
			_, _ = sw.Write([]byte("parcial\n"))
			_ = buf.Flush()

			gone := make(chan struct{})
			go func() {
				_, _ = io.Copy(io.Discard, conn)
				close(gone)
			}()
			select {
			case <-gone:
			case <-release:
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	c, err := NewLocal("main", "Main")
	if err != nil {
		t.Fatalf("NewLocal contra o stub: %v", err)
	}
	return c
}

// TestExecRespeitaPrazoComStreamAberto prova que Exec() fecha a leitura do
// attach quando o ctx expira, mesmo com o outro lado do stream nunca
// fechando por conta própria (ping, tail -f, top -b). Sem isso, a goroutine
// do handler, a conexão com o daemon/proxy e o processo no container ficam
// pendurados para sempre, e o prazo de 60s do /exec é só decorativo.
func TestExecRespeitaPrazoComStreamAberto(t *testing.T) {
	c := execHijackStubPendurado(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	type result struct {
		out  string
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		out, code, err := c.Exec(ctx, "web", "ping -c 1000 1.1.1.1")
		done <- result{out, code, err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Exec retornou erro inesperado: %v", r.err)
		}
		if r.code != -1 {
			t.Fatalf("exit code = %d, quer -1 (prazo expirado não pode virar sucesso)", r.code)
		}
		if !strings.Contains(r.out, "parcial") {
			t.Fatalf("saída parcial recebida antes do corte não chegou: %q", r.out)
		}
		if !strings.Contains(r.out, "interrompido") {
			t.Fatalf("saída não avisa que o exec foi interrompido pelo prazo: %q", r.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Exec ignorou o prazo do ctx e ficou pendurado")
	}
}

// TestExecExitCodeMenosUmQuandoExecAindaRodando prova que Exec() não lê
// ExitCode == 0 como sucesso enquanto o daemon ainda não terminou o exec: o
// attach ter dado EOF (stdout/stderr fechados) não prova que o processo
// morreu, e Running == true é o sinal de que o daemon ainda não gravou o
// exit code real. A saída visível ao operador também carrega o aviso.
func TestExecExitCodeMenosUmQuandoExecAindaRodando(t *testing.T) {
	c := execHijackStub(t, 0, http.StatusOK, true)
	out, code, err := c.Exec(context.Background(), "web", "exec >/dev/null 2>&1; sleep 60")
	if err != nil {
		t.Fatalf("Exec retornou erro inesperado: %v", err)
	}
	if code != -1 {
		t.Fatalf("exit code = %d, quer -1 (exec ainda Running não pode virar 'sucesso')", code)
	}
	if !strings.Contains(out, "DESCONHECIDO") {
		t.Fatalf("saída não avisa que o exit code é desconhecido: %q", out)
	}
}
