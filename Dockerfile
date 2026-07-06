# syntax=docker/dockerfile:1

# ---- Build stage ----
# BUILDPLATFORM/TARGETARCH permitem build multi-arch via buildx. Sem buildx,
# TARGETARCH cai no arch nativo do host — arm64 nas Oracle Ampere.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
LABEL authors="the-eduardo"

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /app

# Cache de dependências: baixa módulos antes de copiar o resto do código.
# go.sum* (opcional) evita quebrar o build antes do primeiro `go mod tidy`.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-w -s" -o /out/bot ./cmd/bot

# ---- Final stage ----
FROM alpine:3.20
# openssh-client: usado pelo connection helper para falar com hosts remotos
# (docker system dial-stdio por SSH).
RUN apk add --no-cache ca-certificates tzdata openssh-client

WORKDIR /app
COPY --from=builder /out/bot /app/bot

# Roda como root: o acesso ao /var/run/docker.sock (root:docker) exige isso.
# O container é isolado e, tendo acesso ao socket, já teria controle do host de
# qualquer forma — então dropar privilégio aqui não agregaria segurança real.
ENTRYPOINT ["/app/bot"]
