// Package system coleta métricas do host (CPU, RAM, disco, uptime) via gopsutil,
// substituindo os antigos exec de mpstat/free/uptime. Dentro de um container sem
// limites, /proc reflete o host, então os números correspondem à máquina real —
// o mesmo comportamento da versão anterior, porém sem depender de binários.
package system

import (
	"context"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// Stats agrega as métricas gerais do host.
type Stats struct {
	CPUPercent float64
	MemUsed    uint64
	MemTotal   uint64
	DiskUsed   uint64
	DiskTotal  uint64
	Uptime     time.Duration
}

// Collect coleta as métricas do host. `diskPath` define qual filesystem medir
// (ex.: "/" ou um caminho montado do host). Falhas individuais não abortam a
// coleta: o campo correspondente fica zerado.
func Collect(ctx context.Context, diskPath string) *Stats {
	s := &Stats{}

	// A amostra de 500ms é necessária para o percentual de CPU fazer sentido.
	if pcts, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, false); err == nil && len(pcts) > 0 {
		s.CPUPercent = pcts[0]
	}
	if vm, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		s.MemUsed = vm.Used
		s.MemTotal = vm.Total
	}
	if du, err := disk.UsageWithContext(ctx, diskPath); err == nil {
		s.DiskUsed = du.Used
		s.DiskTotal = du.Total
	}
	if up, err := host.UptimeWithContext(ctx); err == nil {
		s.Uptime = time.Duration(up) * time.Second
	}

	return s
}
