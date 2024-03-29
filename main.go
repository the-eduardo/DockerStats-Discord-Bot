package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	//"time"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

	"github.com/bwmarrin/discordgo"
)

type Config struct {
	DiscordToken   string
	DiscordOwnerID string
	CommandPrefix  string
	Command        string
	TimeOutSeconds int
	Hostname       string
}

func readConfig() *Config {
	var cfg Config
	cfg.DiscordToken = os.Getenv("DISCORD_TOKEN")
	cfg.CommandPrefix = os.Getenv("COMMAND_PREFIX")
	cfg.Command = os.Getenv("COMMAND")
	getTimout := os.Getenv("SHUTDOWN_TIMEOUT")
	cfg.Hostname = os.Getenv("HOSTNAME")
	cfg.DiscordOwnerID = os.Getenv("DISCORD_OWNER_ID")

	if cfg.DiscordToken == "" || cfg.DiscordOwnerID == "" {
		log.Fatal("DISCORD environment variables must be set")
	}
	if cfg.CommandPrefix == "" {
		cfg.CommandPrefix = "!"
	}
	if cfg.Command == "" {
		cfg.Command = "vm"
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "Machine"
	}
	TimeOutSeconds, err := strconv.Atoi(getTimout)
	if err != nil || TimeOutSeconds < 0 || TimeOutSeconds > 60 {
		cfg.TimeOutSeconds = 10
	}
	cfg.TimeOutSeconds = TimeOutSeconds

	return &cfg
}
func main() {
	cfg := readConfig()
	// Create a new Discord session using the bot token
	dg, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		fmt.Println("Error creating Discord session: ", err)
		return
	}

	// Register the messageCreate func as a callback for MessageCreate events
	dg.AddHandler(messageCreate)

	// Open a websocket connection to Discord and begin listening for events
	err = dg.Open()
	if err != nil {
		log.Println("Error opening Discord connection: ", err)
		return
	}

	log.Println("Bot is running!")

	// Wait indefinitely
	select {}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	cfg := readConfig()
	// Ignore messages sent by the bot itself
	if m.Author.ID == s.State.User.ID || m.Author.ID != cfg.DiscordOwnerID {
		return
	}

	// Check if the message starts with the command prefix
	if !strings.HasPrefix(m.Content, cfg.CommandPrefix) {
		return
	}

	// Split the command into its parts
	parts := strings.Fields(m.Content)

	// Check if the command matches the expected command
	if parts[0] != cfg.CommandPrefix+cfg.Command {
		return
	}
	if parts[0] == cfg.CommandPrefix+cfg.Command {
		if len(parts) > 1 {
			command := parts[1]
			containerName := parts[2]

			var response string
			switch command {
			case "start":
				response = startContainer(containerName)
			case "restart":
				response = restartContainer(containerName, cfg)
			case "stop":
				response = stopContainer(containerName, cfg)
			default:
				response = "Unknown command: " + command + "\nAvailable commands are: start, restart, stop"
			}
			//if containerName == "" { // Don't work.
			//	s.ChannelMessageSend(m.ChannelID, "Please specify a container name")
			//	return
			//}
			s.ChannelMessageSend(m.ChannelID, response)
			return
		}
	}

	// Fetch the system stats
	log.Println("Checking stats...")
	stats, err := getSystemStats()
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error fetching system stats: "+err.Error())
		return
	}

	dockerStatus, err := getDockerStatus()
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error fetching docker stats: "+err.Error())
		return
	}
	// Send the stats to the Discord channel
	s.ChannelMessageSend(m.ChannelID, "# "+cfg.Hostname+"\n> - CPU Usage: "+stats.cpuUsage+" | Memory Usage: "+stats.memUsage+" - "+stats.maxMem+"\n> - Uptime: "+stats.uptime+"## Docker Status:\n"+dockerStatus)
}

type systemStats struct {
	cpuUsage string
	memUsage string
	maxMem   string
	uptime   string
}

func getSystemStats() (*systemStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	// Get the CPU usage percentage
	cpuCmd := exec.CommandContext(ctx, "bash", "-c", "mpstat 1 1 | awk '$12 ~ /[0-9.]+/ { print 100 - $12 }'")
	cpuUsageBytes, err := cpuCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error getting CPU usage: %v", err)
	}
	cpuScanner := bufio.NewScanner(bytes.NewReader(cpuUsageBytes))
	var cpuUsage float64
	if cpuScanner.Scan() {
		cpuUsage, err = strconv.ParseFloat(cpuScanner.Text(), 64)
		if err != nil {
			return nil, fmt.Errorf("error parsing CPU usage: %v", err)
		}
	}

	// Get the memory usage in MB
	memCmd := exec.CommandContext(ctx, "bash", "-c", "free -m | awk '/Mem:/ { print $3\"MB\" }'")
	memUsageBytes, err := memCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error getting memory usage: %v", err)
	}
	memUsage := strings.TrimSpace(string(memUsageBytes))

	// Get the maximum available memory in GB
	maxMemCmd := exec.CommandContext(ctx, "bash", "-c", "free -m | awk '/Mem:/ { print $2 }'")
	maxMemBytes, err := maxMemCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error getting max memory: %v", err)
	}
	maxMem, err := strconv.ParseFloat(strings.TrimSpace(string(maxMemBytes)), 64)
	if err != nil {
		return nil, fmt.Errorf("error parsing max memory: %v", err)
	}
	maxMemGB := strconv.FormatFloat(maxMem/1024, 'f', 2, 64) + "GB"

	// Get the system uptime

	uptimeCmd := exec.CommandContext(ctx, "bash", "-c", "uptime -p")
	uptimeBytes, err := uptimeCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error getting system uptime: %v", err)
	}

	// Format the uptime as a string
	uptimeStr := string(uptimeBytes)

	return &systemStats{
		cpuUsage: strconv.FormatFloat(cpuUsage, 'f', 2, 64) + "%",
		memUsage: memUsage,
		maxMem:   maxMemGB,
		uptime:   uptimeStr,
	}, nil
}

func getDockerStatus() (string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return "", fmt.Errorf("error creating Docker client: %v", err)
	}

	containers, err := cli.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		return "", fmt.Errorf("error getting myContainer list: %v", err)
	}

	var sb strings.Builder
	for _, myContainer := range containers {
		// Remove the "/" prefix from the container name
		myContainer.Names[0] = strings.TrimPrefix(myContainer.Names[0], "/")
		// Get only the first 12 characters of the container ID
		shortContainerID := myContainer.ID[:12]

		sb.WriteString(fmt.Sprintf("- **%s**\n - Status %s | ID: %s\n", myContainer.Names[0], myContainer.Status, shortContainerID))
	}

	return sb.String(), nil
}

func startContainer(containerName string) string {
	// Create a Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return "Error creating Docker client: " + err.Error()
	}
	// Check if the container exists
	_, err = cli.ContainerInspect(context.Background(), containerName)
	if err != nil {
		return "Error starting container " + containerName + ": Container not found"
	}

	// Start the container by name
	err = cli.ContainerStart(context.Background(), containerName, types.ContainerStartOptions{})
	if err != nil {
		return "Error starting container " + containerName + ": " + err.Error()
	}

	return "Container " + containerName + " started."
}

func restartContainer(containerName string, cfg *Config) string {
	// Create a Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return "Error creating Docker client: " + err.Error()
	}

	_, err = cli.ContainerInspect(context.Background(), containerName)
	if err != nil {
		return "Error restarting container " + containerName + ": Container not found"
	}

	// Restart the container by name
	err = cli.ContainerRestart(context.Background(), containerName, container.StopOptions{
		Signal:  "SIGINT",
		Timeout: &cfg.TimeOutSeconds, // Use the integer value directly
	})
	if err != nil {
		return "Error restarting container " + containerName + ": " + err.Error()
	}

	return "Container " + containerName + " restarted."
}

func stopContainer(containerName string, cfg *Config) string {
	// Create a Docker client
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return "Error creating Docker client: " + err.Error()
	}

	_, err = cli.ContainerInspect(context.Background(), containerName)
	if err != nil {
		return "Error stopping container " + containerName + ": Container not found"
	}

	// Stop the container by name
	err = cli.ContainerStop(context.Background(), containerName, container.StopOptions{
		Signal:  "SIGINT",
		Timeout: &cfg.TimeOutSeconds,
	})
	if err != nil {
		return "Error stopping container " + containerName + ": " + err.Error()
	}

	return "Container " + containerName + " stopped."
}
