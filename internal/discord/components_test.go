package discord

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/the-eduardo/DockerStats-Discord-Bot/internal/dockerx"
)

func containersComNome(n int, prefixo string) []dockerx.Container {
	list := make([]dockerx.Container, 0, n)
	for i := 0; i < n; i++ {
		list = append(list, dockerx.Container{
			Name:  prefixo,
			State: "running",
		})
	}
	return list
}

// Regressão real medida em 11/08/2026: host local com 22 containers e host
// remoto (Master) com poucos — sem cota, o local consumia quase todas as 25
// opções e o Master quase sumia do select. Aqui simulamos o pior caso: local
// com muito mais containers do que cabe, Master com poucos.
func TestBuildSelectOptionsGarenteCotaParaHostComPoucosContainers(t *testing.T) {
	hosts := []hostContainers{
		{key: "main", label: "Oracle Main", containers: containersComNome(22, "local")},
		{key: "master", label: "Oracle Master", containers: containersComNome(5, "master")},
	}

	options := buildSelectOptions(hosts, true)

	if len(options) != maxSelectOptions {
		t.Fatalf("esperava %d opcoes (teto do Discord), veio %d", maxSelectOptions, len(options))
	}

	fromMaster := 0
	for _, o := range options {
		hostKey, _ := parseTarget(o.Value)
		if hostKey == "master" {
			fromMaster++
		}
	}
	// Com quota=25/2=12 e o Master tendo só 5, TODOS os 5 devem entrar — é
	// exatamente o cenário que a cota existe para proteger, contra a versão
	// sequencial antiga onde o Master ficaria com o que sobrasse por último.
	if fromMaster != 5 {
		t.Fatalf("esperava os 5 containers do master presentes (cota), veio %d", fromMaster)
	}
}

func TestBuildSelectOptionsSemEstouroQuandoCabeTudo(t *testing.T) {
	hosts := []hostContainers{
		{key: "main", label: "Oracle Main", containers: containersComNome(10, "local")},
		{key: "master", label: "Oracle Master", containers: containersComNome(5, "master")},
	}

	options := buildSelectOptions(hosts, true)

	if len(options) != 15 {
		t.Fatalf("esperava 15 opcoes (nada sobra quando cabe tudo), veio %d", len(options))
	}
}

func TestBuildSelectOptionsSemHosts(t *testing.T) {
	options := buildSelectOptions(nil, false)
	if len(options) != 0 {
		t.Fatalf("esperava lista vazia sem hosts, veio %d", len(options))
	}
}

func TestBuildSelectOptionsUmHostSoRespeitaTeto(t *testing.T) {
	hosts := []hostContainers{
		{key: "main", label: "Oracle Main", containers: containersComNome(40, "local")},
	}

	options := buildSelectOptions(hosts, false)

	if len(options) != maxSelectOptions {
		t.Fatalf("esperava %d opcoes, veio %d", maxSelectOptions, len(options))
	}
}

// nomeGigante devolve um nome de container com n runes (acentuado de
// propósito: o teto do Discord é em caracteres, não em bytes).
func nomeGigante(prefixo string, n int) string {
	r := []rune(prefixo)
	for len(r) < n {
		r = append(r, 'ã')
	}
	return string(r[:n])
}

// assertValuesUnicosEAteCem falha se algum Value repetir (o Discord rejeita
// o select inteiro) ou passar do teto de 100 runes.
func assertValuesUnicosEAteCem(t *testing.T, values []string) {
	t.Helper()
	vistos := make(map[string]int, len(values))
	for i, v := range values {
		if n := len([]rune(v)); n > maxSelectValue {
			t.Errorf("opção %d tem Value com %d runes (> %d): %q", i, n, maxSelectValue, v)
		}
		if j, dup := vistos[v]; dup {
			t.Errorf("Value duplicado nas opções %d e %d: %q", j, i, v)
		}
		vistos[v] = i
	}
}

func valuesDe(t *testing.T, hosts []hostContainers, multiHost bool) []string {
	t.Helper()
	opts := buildSelectOptions(hosts, multiHost)
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Value)
	}
	return out
}

// Value do select acima de 100 runes era TRUNCADO -- e truncar muda o ALVO
// (o prefixo pode ser o nome exato de outro container). Agora é omitido,
// como já era no autocomplete (toChoice). Um host só: [A, gigante, B, C].
// Com um `continue` ingênuo nas passadas, taken=3 e a passada 2 fatiaria
// containers[3:] = [C], duplicando C -- por isso a unicidade é afirmada aqui.
func TestBuildSelectOptionsOmiteValueAcimaDe100UmHost(t *testing.T) {
	gigante := nomeGigante("gigante-", 120)
	hosts := []hostContainers{{key: "main", label: "Oracle Main", containers: []dockerx.Container{
		{Name: "A", State: "running"},
		{Name: gigante, State: "running"},
		{Name: "B", State: "running"},
		{Name: "C", State: "running"},
	}}}

	values := valuesDe(t, hosts, false)

	assertValuesUnicosEAteCem(t, values)
	for _, v := range values {
		if _, n := parseTarget(v); strings.HasPrefix(gigante, n) {
			t.Fatalf("container gigante (ou um prefixo truncado dele) entrou no select: %q", v)
		}
	}
	want := []string{"main:A", "main:B", "main:C"}
	if strings.Join(values, "|") != strings.Join(want, "|") {
		t.Fatalf("opções = %v, queria %v (ordem preservada, gigante omitido)", values, want)
	}
}

// Multi-host com cota: 2 hosts x 30 containers, gigantes no MEIO da faixa da
// cota (25/2=12) de cada host. O omitido não pode consumir cota nem
// desalinhar a passada 2 (duplicata), e todo container curto que cabe entra.
func TestBuildSelectOptionsOmiteValueAcimaDe100MultiHost(t *testing.T) {
	mk := func(key, prefixo string) []dockerx.Container {
		list := make([]dockerx.Container, 0, 30)
		for i := 0; i < 30; i++ {
			name := fmt.Sprintf("%s%02d", prefixo, i)
			if i == 3 || i == 7 || i == 11 {
				name = nomeGigante(fmt.Sprintf("%s%02d-", prefixo, i), 130)
			}
			list = append(list, dockerx.Container{Name: name, State: "running"})
		}
		return list
	}
	hosts := []hostContainers{
		{key: "main", label: "Oracle Main", containers: mk("main", "app-")},
		{key: "master", label: "Oracle Master", containers: mk("master", "wk-")},
	}

	values := valuesDe(t, hosts, true)

	if len(values) != maxSelectOptions {
		t.Fatalf("esperava %d opções (sobram 27 curtos por host), veio %d", maxSelectOptions, len(values))
	}
	assertValuesUnicosEAteCem(t, values)

	porHost := map[string]int{}
	for _, v := range values {
		k, n := parseTarget(v)
		porHost[k]++
		if len([]rune(n)) > 10 {
			t.Fatalf("nome gigante (ou truncado) entrou no select: %q", v)
		}
	}
	// cota 12 de cada + 1 de sobra para o 1º host (ordem original).
	if porHost["main"] != 13 || porHost["master"] != 12 {
		t.Fatalf("distribuição errada: main=%d master=%d (esperava 13/12)", porHost["main"], porHost["master"])
	}
}

// Fiação: componentsFrom (o que render() publica) usa buildSelectOptions; se
// TODOS os containers forem gigantes, o select cai no placeholder inerte em
// vez de publicar um Value truncado.
func TestComponentsFromTodosGigantesViraPlaceholder(t *testing.T) {
	b := &Bot{}
	hosts := []hostContainers{{key: "main", label: "Oracle Main", containers: []dockerx.Container{
		{Name: nomeGigante("x-", 101), State: "running"},
		{Name: nomeGigante("y-", 150), State: "exited"},
	}}}

	comps := b.componentsFrom(hosts)
	row, ok := comps[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("1º componente não é ActionsRow: %T", comps[0])
	}
	sel, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok {
		t.Fatalf("1º item da linha não é SelectMenu: %T", row.Components[0])
	}
	if len(sel.Options) != 1 || sel.Options[0].Value != "_none" || !sel.Disabled {
		t.Fatalf("esperava só o placeholder desabilitado _none, veio disabled=%v opções=%+v", sel.Disabled, sel.Options)
	}
}
