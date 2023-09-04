# Docker Stats - Discord Bot

## Overview

The Bot is a basic tool that allows you to monitor and manage Docker containers on Discord. It provides Docker container statistics and commands to start, restart, and stop.

## Table of Contents
1. [Requirements](#requirements)
2. [Installation](#installation)
3. [Available Commands](#available-commands)
4. [Note](#note)
5. [To-Do List](#to-do-list)
6. [License](#license)

You can use these links to navigate directly to the respective sections in your README file.
## Requirements

Before you can run it, you need to have installed and configured:

- ### Docker
Docker is a platform that enables developers to build, share, and run applications in containers.
If you don't have Docker installed already, you can download it from the [official Docker website](https://docs.docker.com/get-docker/).
- ### Discord Bot Token 
You can obtain it here: [Discord Developer Portal](https://discord.com/developers/docs/intro).
## Installation

Follow these steps to set up and run the Docker Stats Discord Bot:

1. Clone this repository to your local machine:

   ```bash
   git clone https://github.com/the-eduardo/DockerStats-Discord-Bot/
   ```

2. Navigate to the cloned repository:

   ```bash
   cd DockerStats-Discord-Bot
   ```

3. Configuration
- Before running the bot, make sure to add your credentials on `docker-compose.yml` file:
```yml
      DISCORD_TOKEN: YOUR_DISCORD_TOKEN_HERE
      DISCORD_OWNER_ID: YOUR_DISCORD_ID_HERE
      COMMAND_PREFIX:  # What will trigger the bot commands. Default is !
      COMMAND:  # General !command. Default is vm
      SHUTDOWN_TIMEOUT: # Set the waiting time for graceful shutdown when stopping and restarting docker containers. Default is 10 seconds
      HOSTNAME: "Main Machine" # Your machine name, used to identify the host in the bot's messages
```

   By default, the command prefix is `!` and the command is `vm`.
   Note that you can run the bot in multiple machines with the same token and configs.

4. Run the Bot using docker compose:

   ```bash
   docker-compose up -d --build
   ```

5. To stop the bot, run:

   ```bash
   docker-compose down
   ```

## Available commands:
### !vm - Get system and docker stats
### !vm start <container_name> - Start a docker container
### !vm restart <container_name> - Restart a docker container
### !vm stop <container_name> - Stop a docker container

## Note

The Dockerfile is currently configured to build an application image compatible with the `arm64` Linux architecture. This is specified in the Go build command within the Dockerfile using the `GOARCH=arm64` and `GOOS=linux` environment variables.

When your target environment differs, you **WILL** need to modify these settings in the Dockerfile. Here's a detailed guide on how to adapt the Dockerfile for different architectures and operating systems:

### Changing the Architecture

The `GOARCH` environment variable sets the target architecture for the build. If you are targeting a different architecture, replace `arm64` with your target architecture. Go supports multiple architectures, such as `amd64` for x86-64, `386` for x86, `arm` for 32-bit ARM, and `arm64` for 64-bit ARM, among others.

### Changing the Operating System

The `GOOS` environment variable sets the target operating system for the build. If you are targeting a different operating system, replace `linux` with your target OS. Go supports various operating systems, including `windows`, `darwin` (for macOS), `linux`, and more.

Here's an example of how to modify the Dockerfile to build for the `amd64` architecture on a Linux system:

```Dockerfile
FROM golang:latest
LABEL authors="the-eduardo"
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . ./

# Build the application
RUN CGO_ENABLED=0 GOARCH=amd64 GOOS=linux go build -o app
EXPOSE 8080
CMD ["./app"]
```

In this example, `GOARCH=amd64` and `GOOS=linux` are set to build for 64-bit Linux systems.

Always verify the resulting Docker image. You can use the `docker run` command to run the image and check if it works as expected.
## To-Do List

Here are some features and improvements that can be added in the future:

- Allow other users and discord roles to use its commands.
- Implement a configuration system to change the command prefix and other settings inside the Discord.
- Add an option to automatically update a message at regular intervals, displaying real-time information.
- Add new commands: Build images, remove containers, get and build new images, etc.

# Feel free to contribute!

## About the Code

The code is written in Go and uses the Docker SDK to interact with Docker containers. It also utilizes the DiscordGo library to create and manage a Discord bot. The bot can fetch system stats and Docker container information and execute various Docker container management commands.

## License

This project is licensed under the Apache License 2.0. See the [LICENSE](https://github.com/the-eduardo/DockerStats-Discord-Bot/blob/main/LICENSE) file for details.

---

**Disclaimer**: Use this bot responsibly and ensure that you have the necessary permissions to interact with Docker and Discord services. The bot owner is responsible for its actions.
