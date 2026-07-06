# DockerStats Discord Bot — Dashboard

Bot privado de Discord para **monitorar e controlar containers Docker** de uma
máquina direto pelo celular. Evolução da versão original em `main.go` único,
agora estruturado em camadas e usando **slash commands**.

> Estado atual: **Fase 3** (controle total). Painel persistente + controles
> interativos com **pause/unpause**, **logs** e **exec** (via modal), com
> **confirmação** nas ações destrutivas. O controle das duas VPS de um bot só
> (multi-host) chega na Fase 4.

## Arquitetura

```
cmd/bot/            entrypoint
internal/config/    carrega e valida as variáveis de ambiente
internal/dockerx/   camada Docker (list, start/stop/restart, stats por container)
internal/system/    métricas do host via gopsutil (CPU, RAM, disco, uptime)
internal/store/     persiste a referência do painel (canal + mensagem) em JSON
internal/discord/   sessão, slash commands, painel e componentes interativos
```

## Dashboard

Rode `/dashboard` no canal desejado. O bot fixa uma mensagem que ele **edita a
cada `REFRESH_SECONDS`** (padrão 60s) com:

- **Host**: CPU %, RAM usada/total, disco usado/total, uptime.
- **Containers**: estado (🟢/🟡/🔴), CPU % e RAM de cada um.

A mensagem traz controles:

- um **menu** para escolher um container; ao escolher, aparecem (só para você)
  botões **cientes do estado**: rodando → *Reiniciar / Pausar / Parar*; pausado
  → *Retomar / Parar*; parado → *Iniciar*; e sempre um botão **📜 Logs**;
- **Parar** e **Reiniciar** pedem **confirmação** (✅/✖️, expira em 30s);
- um botão **🔄 Atualizar agora** para forçar o refresh.

A referência do painel é persistida (volume `dsbot-data`), então após um restart
o bot volta a editar a mesma mensagem. Se ela for apagada, é recriada.

## Comandos

| Comando                    | O que faz                                             |
|----------------------------|-------------------------------------------------------|
| `/dashboard`               | Fixa o painel auto-atualizável neste canal            |
| `/status`                  | Envia um snapshot pontual do host + containers        |
| `/start <container>`       | Inicia um container (com autocomplete de nome)        |
| `/stop <container>`        | Para um container de forma graceful                   |
| `/restart <container>`     | Reinicia um container                                 |
| `/pause <container>`       | Pausa (suspende) um container                         |
| `/unpause <container>`     | Retoma um container pausado                           |
| `/logs <container> [lines]`| Últimos logs (anexo `.log` quando grande)             |
| `/exec <container>`        | Abre um modal para rodar um comando (via `sh -c`)     |

Todos os comandos são restritos ao `DISCORD_OWNER_ID` e ficam ocultos para
outros membros (`DefaultMemberPermissions = 0`).

> **Segurança**: `/exec` dá um shell dentro dos containers via Discord. O acesso
> é limitado ao seu `OWNER_ID` e à guild privada, mas trate a conta do Discord
> como credencial de acesso às VPS. Audit log e mitigações extras vêm na Fase 5.

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
