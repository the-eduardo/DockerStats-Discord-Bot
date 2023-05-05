package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	//"time"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"

	"github.com/bwmarrin/discordgo"
)

const (
	token   = "YOUR_BOT_TOKEN_HERE" // Replace this with your bot token
	prefix  = "!"                   // Set a prefix for your bot commands
	command = "vm"                  // Set the command for your bot
	// Example: !vm
)

func main() {
	// Create a new Discord session using the bot token
	dg, err := discordgo.New("Bot " + token)
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
	// Ignore messages sent by the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Check if the message starts with the command prefix
	if !strings.HasPrefix(m.Content, prefix) {
		return
	}

	// Split the command into its parts
	parts := strings.Fields(m.Content)

	// Check if the command matches the expected command
	if parts[0] != prefix+command {
		return
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
	s.ChannelMessageSend(m.ChannelID, "CPU Usage: "+stats.cpuUsage+" | Memory Usage: "+stats.memUsage+" - "+stats.maxMem+" | Uptime: "+stats.uptime+"\n\nDocker Status:\n"+dockerStatus)
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
		return "", fmt.Errorf("error getting container list: %v", err)
	}

	var sb strings.Builder
	for _, container := range containers {
		sb.WriteString(fmt.Sprintf("Container Name: %s\n", container.Names[0]))
		sb.WriteString(fmt.Sprintf("Status: %s\n", container.Status))
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
