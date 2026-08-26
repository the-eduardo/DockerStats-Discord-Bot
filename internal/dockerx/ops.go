package dockerx

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// State devolve o estado atual do container (running, paused, exited...).
func (c *Client) State(ctx context.Context, name string) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	return info.State.Status, nil
}

// Pause suspende os processos do container.
func (c *Client) Pause(ctx context.Context, name string) error {
	if err := c.ensureExists(ctx, name); err != nil {
		return err
	}
	return c.cli.ContainerPause(ctx, name)
}

// Unpause retoma um container pausado.
func (c *Client) Unpause(ctx context.Context, name string) error {
	if err := c.ensureExists(ctx, name); err != nil {
		return err
	}
	return c.cli.ContainerUnpause(ctx, name)
}

// maxLogBytes limita quanto de log fica retido em memória durante a leitura.
// Precisa ficar ACIMA do discord.maxAttach (7 MiB): se ficar igual ou abaixo,
// o corte alinhado em rune/linha do tailBytes do pacote discord nunca roda
// (o texto já chega pronto), e um corte no meio de um rune multibyte pode
// sair como UTF-8 inválido no anexo.
const maxLogBytes = 8 << 20

// maxExecBytes limita o pico de memoria do /exec. So os ultimos 1850 bytes
// (discord.maxBlock) chegam ao Discord, entao 1 MiB e ~560x a folga necessaria
// e ainda assim impede que um comando tagarela estoure o mem_limit de 256m.
const maxExecBytes = 1 << 20

// tailWriter é um io.Writer que retém só os últimos max bytes escritos,
// descartando o excedente pela frente. Usado para limitar o pico de memória
// de Logs/Exec sem alterar o volume de dados lido do daemon Docker: o demux
// do stdcopy continua consumindo o stream inteiro, só que sem acumular tudo
// num buffer sem teto.
type tailWriter struct {
	max int
	buf []byte
}

// Write sempre devolve len(p), nil — nunca sinaliza short write. Um short
// write faria o stdcopy.StdCopy abortar com erro no meio da leitura.
func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n >= w.max {
		w.buf = append(w.buf[:0], p[n-w.max:]...)
		return n, nil
	}
	keep := w.max - n
	if len(w.buf) > keep {
		copy(w.buf, w.buf[len(w.buf)-keep:])
		w.buf = w.buf[:keep]
	}
	w.buf = append(w.buf, p...)
	return n, nil
}

func (w *tailWriter) String() string {
	return string(w.buf)
}

// Logs retorna os logs do container gerados dentro da janela `since` (ex.: os
// últimos 30 min). Usa Since em vez de Tail de propósito: em algumas versões do
// daemon o leitor de `--tail` trava em containers em execução, enquanto o
// `--since` é confiável. Faz o demux de stdout/stderr quando não há TTY.
func (c *Client) Logs(ctx context.Context, name string, since time.Duration) (string, error) {
	info, err := c.cli.ContainerInspect(ctx, name)
	if err != nil {
		if client.IsErrNotFound(err) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("inspect %s: %w", name, err)
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

	tw := &tailWriter{max: maxLogBytes}
	if info.Config != nil && info.Config.Tty {
		_, err = io.Copy(tw, rc)
	} else {
		_, err = stdcopy.StdCopy(tw, tw, rc)
	}
	if err != nil && err != io.EOF {
		return tw.String(), err
	}
	return tw.String(), nil
}

// execOutput faz o demux do stream de exec num tailWriter com teto.
func execOutput(r io.Reader, max int) (string, error) {
	tw := &tailWriter{max: max}
	if _, err := stdcopy.StdCopy(tw, tw, r); err != nil && err != io.EOF {
		return tw.String(), err
	}
	return tw.String(), nil
}

// Exec roda um comando via `sh -c` dentro do container e devolve a saída
// combinada (stdout+stderr), anexando o exit code quando diferente de zero, e
// o próprio exit code separado — para quem grava auditoria não precisar
// reextrair do texto. exitCode == -1 significa "desconhecido": o comando
// pode ter rodado (ou não), mas o ContainerExecInspect que confirmaria o
// resultado falhou (timeout, proxy fora), e -1 nunca deve ser lido como
// sucesso.
func (c *Client) Exec(ctx context.Context, name, cmd string) (out string, exitCode int, err error) {
	if err := c.ensureExists(ctx, name); err != nil {
		return "", -1, err
	}

	idResp, err := c.cli.ContainerExecCreate(ctx, name, container.ExecOptions{
		Cmd:          []string{"/bin/sh", "-c", cmd},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", -1, err
	}

	att, err := c.cli.ContainerExecAttach(ctx, idResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return "", -1, err
	}
	defer att.Close()

	out, err = execOutput(att.Reader, maxExecBytes)
	if err != nil {
		return out, -1, err
	}

	insp, inspErr := c.cli.ContainerExecInspect(ctx, idResp.ID)
	if inspErr != nil {
		return out, -1, nil
	}
	if insp.ExitCode != 0 {
		out += fmt.Sprintf("\n[exit code %d]", insp.ExitCode)
	}
	return out, insp.ExitCode, nil
}
