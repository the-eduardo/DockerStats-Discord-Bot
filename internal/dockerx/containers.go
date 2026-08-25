package dockerx

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Container é a visão simplificada de um container que a camada de Discord usa.
type Container struct {
	ID     string
	Name   string
	Image  string
	State  string // running, exited, paused, created, restarting...
	Status string // texto humano do Docker, ex.: "Up 4 days"

	// Preenchidos apenas quando as métricas são coletadas (List não busca stats).
	CPUPercent float64
	MemUsage   uint64 // bytes (sem cache)
	MemLimit   uint64 // bytes
}

// List retorna todos os containers (inclusive parados), ordenados por nome.
func (c *Client) List(ctx context.Context) ([]Container, error) {
	items, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}

	out := make([]Container, 0, len(items))
	for _, it := range items {
		name := ""
		if len(it.Names) > 0 {
			name = strings.TrimPrefix(it.Names[0], "/")
		}
		out = append(out, Container{
			ID:     it.ID,
			Name:   name,
			Image:  it.Image,
			State:  it.State,
			Status: it.Status,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Names retorna apenas os nomes dos containers (usado no autocomplete).
func (c *Client) Names(ctx context.Context) ([]string, error) {
	list, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, ct := range list {
		names = append(names, ct.Name)
	}
	return names, nil
}

// ensureExists confirma que o container existe antes de agir sobre ele.
// Diferencia "container não existe" (ErrNotFound, 404 do daemon) de qualquer
// outro erro de inspect (SSH caído, socket-proxy fora, timeout de contexto):
// colapsar os dois em ErrNotFound faz o operador ler "não encontrado" quando
// o problema real é o host estar inacessível.
func (c *Client) ensureExists(ctx context.Context, name string) error {
	if _, err := c.cli.ContainerInspect(ctx, name); err != nil {
		if client.IsErrNotFound(err) {
			return ErrNotFound
		}
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	return nil
}

// Start inicia um container pelo nome.
func (c *Client) Start(ctx context.Context, name string) error {
	if err := c.ensureExists(ctx, name); err != nil {
		return err
	}
	return c.cli.ContainerStart(ctx, name, container.StartOptions{})
}

// Stop para um container de forma graceful, respeitando o timeout (segundos).
func (c *Client) Stop(ctx context.Context, name string, timeoutSeconds int) error {
	if err := c.ensureExists(ctx, name); err != nil {
		return err
	}
	return c.cli.ContainerStop(ctx, name, container.StopOptions{Timeout: &timeoutSeconds})
}

// Restart reinicia um container respeitando o timeout (segundos).
func (c *Client) Restart(ctx context.Context, name string, timeoutSeconds int) error {
	if err := c.ensureExists(ctx, name); err != nil {
		return err
	}
	return c.cli.ContainerRestart(ctx, name, container.StopOptions{Timeout: &timeoutSeconds})
}
