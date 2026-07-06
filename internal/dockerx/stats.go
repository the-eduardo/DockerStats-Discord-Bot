package dockerx

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/docker/docker/api/types/container"
)

// ErrNotFound é retornado quando o container informado não existe.
var ErrNotFound = errors.New("container não encontrado")

// CollectStats preenche CPUPercent/MemUsage/MemLimit de cada container em `list`,
// buscando as métricas concorrentemente. Erros por container são silenciosos
// (o container fica com métricas zeradas), pois um container parado não tem stats.
func (c *Client) CollectStats(ctx context.Context, list []Container) {
	var wg sync.WaitGroup
	for i := range list {
		if list[i].State != "running" {
			continue // só containers rodando têm métricas
		}
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cpu, memUsage, memLimit, err := c.stat(ctx, list[idx].ID)
			if err != nil {
				return
			}
			list[idx].CPUPercent = cpu
			list[idx].MemUsage = memUsage
			list[idx].MemLimit = memLimit
		}(i)
	}
	wg.Wait()
}

// stat lê duas amostras do stream de stats (como faz o `docker stats`), de forma
// que PreCPUStats do segundo frame venha preenchido pelo daemon e o cálculo de
// CPU% fique correto (a versão one-shot retorna PreCPUStats zerado).
func (c *Client) stat(ctx context.Context, id string) (cpuPercent float64, memUsage, memLimit uint64, err error) {
	resp, err := c.cli.ContainerStats(ctx, id, true)
	if err != nil {
		return 0, 0, 0, err
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	var v container.StatsResponse
	// Descarta o primeiro frame (só popula o baseline) e usa o segundo.
	if err := dec.Decode(&v); err != nil {
		return 0, 0, 0, err
	}
	if err := dec.Decode(&v); err != nil {
		return 0, 0, 0, err
	}

	return calcCPUPercent(v), calcMemUsage(v.MemoryStats), v.MemoryStats.Limit, nil
}

// calcCPUPercent replica a fórmula do docker stats.
func calcCPUPercent(v container.StatsResponse) float64 {
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)

	onlineCPUs := float64(v.CPUStats.OnlineCPUs)
	if onlineCPUs == 0 {
		onlineCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
	}
	if sysDelta > 0 && cpuDelta > 0 && onlineCPUs > 0 {
		return (cpuDelta / sysDelta) * onlineCPUs * 100.0
	}
	return 0
}

// calcMemUsage subtrai o cache do uso de memória, como o docker stats faz,
// cobrindo cgroup v1 (total_inactive_file) e v2 (inactive_file).
func calcMemUsage(mem container.MemoryStats) uint64 {
	if v, ok := mem.Stats["total_inactive_file"]; ok && v < mem.Usage {
		return mem.Usage - v
	}
	if v, ok := mem.Stats["inactive_file"]; ok && v < mem.Usage {
		return mem.Usage - v
	}
	return mem.Usage
}
