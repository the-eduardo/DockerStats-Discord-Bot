# Docker Stats - Discord Bot

## Overview

The Bot is a basic tool that allows you to monitor and manage Docker containers on Discord. It provides Docker container statistics and commands to start, restart, and stop.

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
