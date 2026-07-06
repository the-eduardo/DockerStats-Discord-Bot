package dockerx

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// State devolve o estado atual do container (running, paused, exited...).
func (c *Client) State(ctx context.Context, name string) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		return "", ErrNotFound
	}
	return info.State.Status, nil
}

// Pause suspende os processos do container.
func (c *Client) Pause(ctx context.Context, name string) error {
	if !c.exists(ctx, name) {
		return ErrNotFound
	}
	return c.cli.ContainerPause(ctx, name)
}

// Unpause retoma um container pausado.
func (c *Client) Unpause(ctx context.Context, name string) error {
	if !c.exists(ctx, name) {
		return ErrNotFound
	}
	return c.cli.ContainerUnpause(ctx, name)
}

// Logs retorna os logs do container gerados dentro da janela `since` (ex.: os
// últimos 30 min). Usa Since em vez de Tail de propósito: em algumas versões do
// daemon o leitor de `--tail` trava em containers em execução, enquanto o
// `--since` é confiável. Faz o demux de stdout/stderr quando não há TTY.
func (c *Client) Logs(ctx context.Context, name string, since time.Duration) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		return "", ErrNotFound
	}

	sinceUnix := strconv.FormatInt(time.Now().Add(-since).Unix(), 10)
	rc, err := c.cli.ContainerLogs(ctx, name, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Since:      sinceUnix,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()

	var buf bytes.Buffer
	if info.Config != nil && info.Config.Tty {
		_, err = io.Copy(&buf, rc)
	} else {
		_, err = stdcopy.StdCopy(&buf, &buf, rc)
	}
	if err != nil && err != io.EOF {
		return buf.String(), err
	}
	return buf.String(), nil
}

// Exec roda um comando via `sh -c` dentro do container e devolve a saída
// combinada (stdout+stderr), anexando o exit code quando diferente de zero.
func (c *Client) Exec(ctx context.Context, name, cmd string) (string, error) {
	if !c.exists(ctx, name) {
		return "", ErrNotFound
	}

	idResp, err := c.cli.ContainerExecCreate(ctx, name, container.ExecOptions{
		Cmd:          []string{"/bin/sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", err
	}

	att, err := c.cli.ContainerExecAttach(ctx, idResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", err
	}
	defer att.Close()

	var buf bytes.Buffer
	if _, err := stdcopy.StdCopy(&buf, &buf, att.Reader); err != nil && err != io.EOF {
		return buf.String(), err
	}

	out := buf.String()
	if insp, err := c.cli.ContainerExecInspect(ctx, idResp.ID); err == nil && insp.ExitCode != 0 {
		out += fmt.Sprintf("\n[exit code %d]", insp.ExitCode)
	}
	return out, nil
}
