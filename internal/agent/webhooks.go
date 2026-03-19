package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type discordMessage struct {
	Content string `json:"content"`
}

func sendDiscordWebhook(webhookURL string, message string) {
	body, err := json.Marshal(discordMessage{Content: message})
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

func buildServerStartedMessage(world string, day int) string {
	msg := "**Server Started**"
	if world != "" {
		msg += fmt.Sprintf(" — %s", world)
	}
	if day > 0 {
		msg += fmt.Sprintf(" (Day %d)", day)
	}
	return msg
}

func buildServerStoppedMessage(uptime string) string {
	msg := "**Server Stopped**"
	if uptime != "" {
		msg += fmt.Sprintf(" — uptime %s", uptime)
	}
	return msg
}

func buildWorldSavedMessage(world string, day int, gameTime string) string {
	msg := "**World Saved**"
	if world != "" {
		msg += fmt.Sprintf(" — %s", world)
	}
	if day > 0 {
		msg += fmt.Sprintf(", Day %d", day)
	}
	if gameTime != "" {
		msg += fmt.Sprintf(" (%s)", gameTime)
	}
	return msg
}
