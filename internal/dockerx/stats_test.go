package dockerx

import (
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestCalcCPUPercent(t *testing.T) {
	cases := []struct {
		nome string
		v    container.StatsResponse
		want float64
	}{
		{
			nome: "caso normal, 1 core online",
			v: container.StatsResponse{Stats: container.Stats{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 300},
					SystemUsage: 2000,
					OnlineCPUs:  1,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 1000,
				},
			}},
			// cpuDelta=200, sysDelta=1000 -> (200/1000)*1*100 = 20%
			want: 20.0,
		},
		{
			nome: "sysDelta == 0 -> guard evita divisao por zero",
			v: container.StatsResponse{Stats: container.Stats{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 300},
					SystemUsage: 1000,
					OnlineCPUs:  1,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 1000,
				},
			}},
			want: 0,
		},
		{
			nome: "cpuDelta == 0 -> container ocioso, sem uso",
			v: container.StatsResponse{Stats: container.Stats{
				CPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 2000,
					OnlineCPUs:  1,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 1000,
				},
			}},
			want: 0,
		},
		{
			nome: "OnlineCPUs == 0 -> cai no fallback de len(PercpuUsage)",
			v: container.StatsResponse{Stats: container.Stats{
				CPUStats: container.CPUStats{
					CPUUsage: container.CPUUsage{
						TotalUsage:  300,
						PercpuUsage: []uint64{1, 2, 3, 4}, // 4 cores
					},
					SystemUsage: 2000,
					OnlineCPUs:  0,
				},
				PreCPUStats: container.CPUStats{
					CPUUsage:    container.CPUUsage{TotalUsage: 100},
					SystemUsage: 1000,
				},
			}},
			// cpuDelta=200, sysDelta=1000 -> (200/1000)*4*100 = 80%
			want: 80.0,
		},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got := calcCPUPercent(c.v)
			if got != c.want {
				t.Errorf("calcCPUPercent() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestCalcMemUsage(t *testing.T) {
	cases := []struct {
		nome string
		mem  container.MemoryStats
		want uint64
	}{
		{
			nome: "sem total_inactive_file nem inactive_file -> usa Usage puro",
			mem:  container.MemoryStats{Usage: 1000},
			want: 1000,
		},
		{
			nome: "cgroup v1 (total_inactive_file) presente e menor que Usage",
			mem: container.MemoryStats{
				Usage: 1000,
				Stats: map[string]uint64{"total_inactive_file": 300},
			},
			want: 700,
		},
		{
			nome: "cgroup v2 (inactive_file) presente e menor que Usage",
			mem: container.MemoryStats{
				Usage: 1000,
				Stats: map[string]uint64{"inactive_file": 400},
			},
			want: 600,
		},
		{
			nome: "total_inactive_file >= Usage -> guard evita underflow, usa Usage puro",
			mem: container.MemoryStats{
				Usage: 1000,
				Stats: map[string]uint64{"total_inactive_file": 5000},
			},
			want: 1000,
		},
		{
			nome: "inactive_file >= Usage -> guard evita underflow, usa Usage puro",
			mem: container.MemoryStats{
				Usage: 1000,
				Stats: map[string]uint64{"inactive_file": 1000},
			},
			want: 1000,
		},
	}

	for _, c := range cases {
		t.Run(c.nome, func(t *testing.T) {
			got := calcMemUsage(c.mem)
			if got != c.want {
				t.Errorf("calcMemUsage() = %v, want %v", got, c.want)
			}
		})
	}
}
