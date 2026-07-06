# DockerStats Discord Bot — Dashboard

Bot privado de Discord para **monitorar e controlar containers Docker** de uma
máquina direto pelo celular. Evolução da versão original em `main.go` único,
agora estruturado em camadas e usando **slash commands**.

> Estado atual: **Fase 1** (fundação). O painel persistente auto-atualizável a
> cada 60s, os botões interativos, `pause`/`logs`/`exec` e o controle das duas
> VPS de um bot só chegam nas fases seguintes.

## Arquitetura

```
cmd/bot/            entrypoint
internal/config/    carrega e valida as variáveis de ambiente
internal/dockerx/   camada Docker (list, start/stop/restart, stats por container)
internal/system/    métricas do host via gopsutil (CPU, RAM, disco, uptime)
internal/discord/   sessão, slash commands, autocomplete e embed do dashboard
```

## Comandos

| Comando               | O que faz                                             |
|-----------------------|-------------------------------------------------------|
| `/status`             | CPU/RAM/disco do host + estado, CPU e RAM por container |
| `/start <container>`  | Inicia um container (com autocomplete de nome)        |
| `/stop <container>`   | Para um container de forma graceful                   |
| `/restart <container>`| Reinicia um container                                 |

Todos os comandos são restritos ao `DISCORD_OWNER_ID` e ficam ocultos para
outros membros (`DefaultMemberPermissions = 0`).

## Configuração

1. Copie `.env.example` para `.env` e preencha `DISCORD_TOKEN`,
   `DISCORD_OWNER_ID` e (recomendado) `DISCORD_GUILD_ID`.
2. Suba com Docker Compose:

   ```bash
   docker compose up -d --build
   ```

3. Logs:

   ```bash
   docker compose logs -f
   ```

### Como pegar os IDs

- **OWNER_ID / GUILD_ID**: ative o *Modo Desenvolvedor* no Discord
  (Configurações → Avançado), clique com o botão direito no seu perfil / no
  ícone do servidor → *Copiar ID*.
- **TOKEN**: [Discord Developer Portal](https://discord.com/developers/applications)
  → sua aplicação → *Bot* → *Reset Token*.

## Notas técnicas

- **Métricas sem shell**: CPU/RAM/uptime vêm do `gopsutil` e o uso por container
  da Docker Stats API (duas amostras, como o `docker stats`) — não dependem mais
  de `mpstat`/`free`.
- **Disco do host**: o compose monta `/:/host:ro` e `DISK_PATH=/host`, então o
  `/status` reporta o disco da máquina, não o overlay do container.
- **Multi-arch**: o Dockerfile usa `TARGETARCH`; nas Oracle Ampere compila arm64
  nativo. Para amd64, `docker buildx build --platform linux/amd64 ...`.

## Licença

Apache License 2.0.
