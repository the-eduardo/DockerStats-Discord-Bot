# Docker Stats - Discord Bot

## Prerequisites
- Linux or another Unix-based system, ideally a Virtual Machine.
- Go (>=1.16), for local building or development.
- Docker
- Discord bot token: [check the official Discord documentation.](https://discord.com/developers/docs/intro)
- *You need to be the Bot owner to use the commands

## Installation
1. Clone this repository:
   ```
   git clone https://github.com/the-eduardo/DockerStats-Discord-Bot/
   ```
   
2. Navigate to the cloned repository:
   ```
   cd DockerStats-Discord-Bot
   ```
   
3. Install the necessary dependencies:
   ```
   go mod tidy
   ```
   

## Configuration
Replace `YOUR_BOT_TOKEN_HERE` in main.go file with your actual bot token.
You also can change the Prefix and the command in this file.

## Usage

### Running as a Docker container

Follow these steps to run the bot within a Docker container:

1. Build the Docker image:
   ```
   sudo docker build -t dockerstats-discord-bot .
   ```
2. Run the container with the following command:
   ```
   sudo docker run -d -v /var/run/docker.sock:/var/run/docker.sock --name dockerstats-bot-container dockerstats-discord-bot
   ```
   Be sure to mount the Docker socket from the host system into the container using the `-v` flag. This will allow your program to connect to the Docker daemon running on the host system and fetch container stats.
 3. In Discord, send `!vm` to any chat to get the system and Docker stats. 

### Or you can run it locally :
1. Build the bot: `go build -o discord-vm-bot main.go`
2. Start the bot: `sudo ./discord-vm-bot`
3. In Discord, send `!vm` to any chat to get the system and Docker stats.

## Todo
- Allow other users besides the Bot owner to use its commands.
- Implement a configuration system to change the command prefix and other settings within Discord.
- Add an option to automatically update a message at regular intervals, displaying real-time information.
- Continuously develop and add new features as needed.
