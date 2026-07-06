// Package dockerx é a camada de acesso ao Docker. Encapsula o SDK oficial para
// que a camada de Discord não dependa diretamente dos tipos do Docker.
//
// Um Client representa UM host Docker. O host local usa o socket
// (/var/run/docker.sock); hosts remotos usam "ssh://user@ip" através do
// connection helper do Docker (que faz `docker system dial-stdio` por SSH).
package dockerx

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"

	"github.com/docker/cli/cli/connhelper/commandconn"
	"github.com/docker/docker/client"
)

// Client é um wrapper fino sobre o client oficial do Docker, com identidade.
type Client struct {
	cli   *client.Client
	Key   string // id estável usado em customIDs (ex.: "main", "master")
	Label string // nome amigável exibido no painel (ex.: "Oracle Main")
}

// NewLocal cria um client para o daemon local (respeita DOCKER_HOST do ambiente).
func NewLocal(key, label string) (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, Key: key, Label: label}, nil
}

// NewRemote cria um client para um host remoto via SSH. `host` deve ser algo
// como "ssh://ubuntu@1.2.3.4[:porta]"; `sshKey` (opcional) é o caminho da chave.
//
// A conexão roda `ssh … sudo docker system dial-stdio` no host remoto: usamos
// `sudo` (sem senha) de propósito, para NÃO precisar alterar o grupo docker do
// host remoto. O daemon remoto é acessado pelo stdio do túnel SSH.
func NewRemote(key, label, host, sshKey string) (*Client, error) {
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("host remoto inválido %q: %v", host, err)
	}

	sshArgs := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=10",
	}
	if sshKey != "" {
		sshArgs = append(sshArgs, "-i", sshKey)
	}
	if p := u.Port(); p != "" {
		sshArgs = append(sshArgs, "-p", p)
	}
	remote := u.Hostname()
	if user := u.User.Username(); user != "" {
		remote = user + "@" + remote
	}
	sshArgs = append(sshArgs, remote, "sudo", "docker", "system", "dial-stdio")

	// Cada conexão abre um `ssh` que faz o túnel stdio até o daemon remoto.
	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return commandconn.New(ctx, "ssh", sshArgs...)
	}

	httpClient := &http.Client{Transport: &http.Transport{DialContext: dialer}}
	cli, err := client.NewClientWithOpts(
		client.WithHTTPClient(httpClient),
		client.WithHost("http://docker.example.com"), // ignorado: o dialer faz o túnel
		client.WithDialContext(dialer),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli, Key: key, Label: label}, nil
}

// Ping verifica se o daemon está acessível.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// Info devolve o nº de CPUs e a memória total (bytes) reportados pelo daemon.
// Usado para hosts remotos, onde não há acesso ao /proc via gopsutil.
func (c *Client) Info(ctx context.Context) (ncpu int, memTotal int64, err error) {
	info, err := c.cli.Info(ctx)
	if err != nil {
		return 0, 0, err
	}
	return info.NCPU, info.MemTotal, nil
}

// Close libera a conexão.
func (c *Client) Close() error { return c.cli.Close() }
