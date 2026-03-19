package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

const (
	colorGreen = 5763719  // 0x57F287
	colorRed   = 15548997 // 0xED4245
	colorBlue  = 5793266  // 0x5865F2
)

type discordEmbed struct {
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Fields      []discordField `json:"fields,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

func sendDiscordWebhook(webhookURL string, embed discordEmbed) {
	payload := discordPayload{Embeds: []discordEmbed{embed}}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Discord webhook: marshal error: %v", err)
		return
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("Discord webhook: send error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("Discord webhook: HTTP %d", resp.StatusCode)
	}
}

func buildServerStartedEmbed(world string, day int) discordEmbed {
	embed := discordEmbed{
		Title:     "Server Started",
		Color:     colorGreen,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if world != "" {
		embed.Fields = append(embed.Fields, discordField{
			Name: "World", Value: world, Inline: true,
		})
	}
	if day > 0 {
		embed.Fields = append(embed.Fields, discordField{
			Name: "Day", Value: fmt.Sprintf("%d", day), Inline: true,
		})
	}
	return embed
}

func buildServerStoppedEmbed(uptime string) discordEmbed {
	embed := discordEmbed{
		Title:     "Server Stopped",
		Color:     colorRed,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if uptime != "" {
		embed.Fields = append(embed.Fields, discordField{
			Name: "Uptime", Value: uptime, Inline: true,
		})
	}
	return embed
}

func buildWorldSavedEmbed(world string, day int, gameTime string) discordEmbed {
	embed := discordEmbed{
		Title:     "World Saved",
		Color:     colorBlue,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	if world != "" {
		embed.Fields = append(embed.Fields, discordField{
			Name: "World", Value: world, Inline: true,
		})
	}
	if day > 0 {
		embed.Fields = append(embed.Fields, discordField{
			Name: "Day", Value: fmt.Sprintf("%d", day), Inline: true,
		})
	}
	if gameTime != "" {
		embed.Fields = append(embed.Fields, discordField{
			Name: "Time", Value: gameTime, Inline: true,
		})
	}
	return embed
}
