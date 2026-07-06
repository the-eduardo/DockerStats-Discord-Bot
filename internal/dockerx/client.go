// Package dockerx é a camada de acesso ao Docker. Encapsula o SDK oficial para
// que a camada de Discord não dependa diretamente dos tipos do Docker.
//
// A struct Client já nasce preparada para múltiplos hosts (Fase 4): hoje só
// conecta no daemon local (via /var/run/docker.sock), mas o construtor aceita
// um host arbitrário, incluindo "ssh://user@ip" para controle remoto.
package dockerx

import (
	"context"

	"github.com/docker/docker/client"
)

// Client é um wrapper fino sobre o client oficial do Docker.
type Client struct {
	cli   *client.Client
	Label string // nome amigável do host (ex.: "Main", "Master")
}

// New cria um client conectado ao daemon local (respeita DOCKER_HOST do ambiente).
func New(label string) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, Label: label}, nil
}

// NewRemote cria um client apontando para um host específico (ex.: ssh://...).
// Reservado para a Fase 4; não é usado ainda.
func NewRemote(label, host string) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, Label: label}, nil
}

// Ping verifica se o daemon está acessível.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// Close libera a conexão.
func (c *Client) Close() error { return c.cli.Close() }
