package discord

import (
	"testing"

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
