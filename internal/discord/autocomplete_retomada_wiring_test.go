package discord

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

// Este arquivo testa a FIAÇÃO da retomada da passada 2 de handleAutocomplete
// (visited, não taken). Os dois contadores só divergem quando a passada 1
// OMITE um nome por estourar o teto de 100 do Value — e o teste de
// autocomplete_quota_wiring_test.go não cobre isso: lá o host do nome gigante
// tem 1 container só, e retomar por taken ou por visited dá o mesmo resultado.
// Aqui o host "main" tem o nome gigante NA FRENTE de 30 normais, e o host
// "master" tem 1 só, então sobram vagas para a passada 2 do "main". Retomando
// por taken, a passada 2 recomeça 1 item antes de onde a 1 parou e emite um
// Value DUPLICADO (o Discord rejeita a resposta inteira com choices repetidas).

// namedContainersHost é como manyContainersHost (render_collect_wiring_test.go),
// mas com a lista EXATA de nomes, na ordem dada — para intercalar um nome
// gigante com nomes normais no mesmo host.
func namedContainersHost(t *testing.T, key, label string, names []string) *dockerx.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.44")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			list := make([]map[string]any, 0, len(names))
			for i, n := range names {
				list = append(list, map[string]any{
					"Id":     fmt.Sprintf("%s-%02d", key, i),
					"Names":  []string{"/" + n},
					"Image":  "nginx",
					"State":  "exited",
					"Status": "Exited (0) 2 minutes ago",
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	t.Cleanup(srv.Close)

	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	h, err := dockerx.NewLocal(key, label)
	if err != nil {
		t.Fatalf("dockerx.NewLocal contra o stub: %v", err)
	}
	return h
}

func TestHandleAutocompleteRetomadaNaoDuplicaAposOmitirGigante(t *testing.T) {
	names := []string{strings.Repeat("g", 140)} // omitido (Value > 100)
	for i := 0; i < 30; i++ {
		names = append(names, fmt.Sprintf("local-%02d", i))
	}
	hostMain := namedContainersHost(t, "main", "Oracle Main", names)
	hostMaster := namedContainersHost(t, "master", "Oracle Master", []string{"remoto-00"})
	b, rt := newAutocompleteWiringBot(t, []*dockerx.Client{hostMain, hostMaster})

	b.handleAutocomplete(autocompleteInteraction(""))

	var payload autocompletePayload
	if err := json.Unmarshal(rt.all(), &payload); err != nil {
		t.Fatalf("payload nao decodificou: %v; corpo: %s", err, rt.all())
	}

	// Controle positivo: a passada 2 tem que ter rodado (13 da passada 1 +
	// sobras até 25), senão a asserção de duplicata passaria vazia.
	if got := len(payload.Data.Choices); got != 25 {
		t.Fatalf("esperava 25 choices (passada 2 preenchendo as sobras), vieram %d -- %+v", got, payload.Data.Choices)
	}
	seen := map[string]bool{}
	for _, c := range payload.Data.Choices {
		if seen[c.Value] {
			t.Fatalf("Value duplicado na resposta do autocomplete (passada 2 retomou do ponto errado): %q -- %+v", c.Value, payload.Data.Choices)
		}
		seen[c.Value] = true
	}
	// Pulo: com retomada correta, local-00..local-23 (24) + remoto-00 = 25.
	for i := 0; i < 24; i++ {
		if v := fmt.Sprintf("main:local-%02d", i); !seen[v] {
			t.Fatalf("container %q sumiu da resposta (passada 2 pulou item) -- %+v", v, payload.Data.Choices)
		}
	}
}
