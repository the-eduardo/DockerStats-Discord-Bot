package discord

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/config"
	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO do log do ping remoto no boot (bot.go,
// pingRemotes). Era o ÚLTIMO sítio da família "%v cru em erro que vem do ssh":
// embed.go:34/:72 fechou em 04/11 e o handleSelect (components.go) em 16/11.
//
// O erro do host remoto vem do commandconn do docker/cli: argv do ssh na
// frente e o `stderr=` do sshd/sudo (até 4096 bytes, com quebras de linha) no
// FIM. Com %v cru, um único erro de boot vira N entradas no json-file (e no
// Loki), e uma linha do stderr pode imitar o prefixo `[dockerstats]`. O sítio
// dispara justamente no reboot do cron com o master fora -- em 12/11 03:00:55Z
// o hostEmbed do master deu "Operation timed out".
//
// O oráculo tem três partes, e cada uma mata uma mutação diferente:
//   - 1 linha  -> exige o %q (truncar sozinho não escapa o \n do tail);
//   - teto     -> exige o truncateMiddle (o %q sozinho não limita o tamanho);
//   - causa    -> exige que o truncate seja o MIDDLE (o prefixo descarta
//     exatamente o `stderr=` que explica a falha).

// pingFalhaHost sobe um stub da API do Docker cujo /_ping falha com a mensagem
// dada. O SDK v27 tenta HEAD primeiro e só cai no GET se o status NÃO for 200
// nem 500 (client/ping.go), então o stub responde 404 no HEAD -- é o GET que
// devolve o 500 com corpo JSON, único caminho em que checkResponseErr lê a
// mensagem do daemon e ela chega inteira ao err.Error().
func pingFalhaHost(t *testing.T, key, label, msg string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/_ping") {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"` + msg + `"}`))
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal(key, label)
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

func TestPingRemotoLogDeErroEmUmaLinhaComCausa(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	// O `\n` da raw string é escape JSON dentro de {"message":"..."}, então o
	// SDK devolve uma quebra de linha DE VERDADE dentro de err.Error(). A
	// linha forjada imita o prefixo do json-file.
	msg := strings.Repeat("x", 1500) +
		`\n2026/11/17 03:00:00 [dockerstats] LINHA-FORJADA\n` +
		"CAUSA-NO-FIM"

	// hosts[0] (o local) nunca é pingado por pingRemotes: basta a identidade.
	local := &dockerx.Client{Key: "main"}
	remote := pingFalhaHost(t, "master", "Master", msg)

	b := &Bot{cfg: &config.Config{}, hosts: []*dockerx.Client{local, remote}}
	b.pingRemotes()

	saida := buf.String()
	// Controle positivo de CAMINHO: sem isto, um stub que respondesse 200 no
	// ping passaria as asserções abaixo pelo motivo errado -- a linha "OK" tem
	// 1 quebra de linha, cabe no teto e não contém nada que o teste proíba.
	if !strings.Contains(saida, "INACESSÍVEL") {
		t.Fatalf("não passou pelo ramo de falha do ping remoto: %.300q", saida)
	}
	if n := strings.Count(saida, "\n"); n != 1 {
		t.Fatalf("erro do ping virou %d linhas no log (esperado 1, o terminador do log.Printf): %.400q", n, saida)
	}
	if !strings.Contains(saida, "CAUSA-NO-FIM") {
		t.Fatalf("log perdeu a causa no fim do erro: %.400q", saida)
	}
	if len(saida) > 600 {
		t.Fatalf("linha de log sem teto: %d bytes; %.200q", len(saida), saida)
	}
}

// TestPingRemotoHostOKNaoLogaInacessivel é o contraponto do controle positivo
// acima: com o ping respondendo 200, o ramo de falha não pode ser alcançado.
func TestPingRemotoHostOKNaoLogaInacessivel(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("API-Version", "1.44")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	remote, err := dockerx.NewLocal("master", "Master")
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}

	b := &Bot{cfg: &config.Config{}, hosts: []*dockerx.Client{{Key: "main"}, remote}}
	b.pingRemotes()

	saida := buf.String()
	if !strings.Contains(saida, `host remoto "master" OK`) {
		t.Fatalf("esperava a linha de host remoto OK, veio: %.300q", saida)
	}
	if strings.Contains(saida, "INACESSÍVEL") {
		t.Fatalf("host saudável não pode logar INACESSÍVEL: %.300q", saida)
	}
}
