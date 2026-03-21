package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"mmcli/internal/agentapi"
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

func buildServerReadyMessage(world string, day int) string {
	msg := "**Server Ready**"
	if world != "" {
		msg += fmt.Sprintf(" — %s is ready to join", world)
	}
	if day > 0 {
		msg += fmt.Sprintf(" (Day %d)", day)
	}
	return msg
}

func buildServerRestartedMessage(uptime string) string {
	msg := "**Server Restarting**"
	if uptime != "" {
		msg += fmt.Sprintf(" — uptime was %s", uptime)
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

func buildPlayerJoinedMessage(player string) string {
	return fmt.Sprintf("**%s** joined the server", player)
}

func buildPlayerLeftMessage(player string) string {
	return fmt.Sprintf("**%s** left the server", player)
}

func buildPlayerDiedMessage(player string) string {
	return fmt.Sprintf("**%s** died", player)
}

func buildPlayerFirstJoinMessage(player string) string {
	return fmt.Sprintf("**%s** joined for the first time!", player)
}

// --- Discord embed types ---

type discordEmbed struct {
	Title     string              `json:"title"`
	Color     int                 `json:"color"`
	Fields    []discordEmbedField `json:"fields"`
	Footer    *discordEmbedFooter `json:"footer,omitempty"`
	Timestamp string              `json:"timestamp,omitempty"`
}

type discordEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type discordEmbedFooter struct {
	Text string `json:"text"`
}

type discordEmbedMessage struct {
	Embeds []discordEmbed `json:"embeds"`
}

const (
	embedColorOnline  = 0x57F287 // green
	embedColorOffline = 0xED4245 // red
	maxFieldLen       = 900      // stay under Discord's 1024 per-field limit
)

func buildStatusEmbed(running bool, status *ModAPIStatus, players []ModAPIPlayer, uptime time.Duration, manifestMods []agentapi.ManifestMod) discordEmbedMessage {
	color := embedColorOffline
	statusVal := "\u274C Offline"
	if running {
		color = embedColorOnline
		statusVal = "\u2705 Online"
	}

	fields := []discordEmbedField{
		{Name: "Status", Value: statusVal, Inline: true},
	}

	// Players count
	if running && status != nil {
		fields = append(fields, discordEmbedField{
			Name: "Players", Value: fmt.Sprintf("%d online", status.PlayerCount), Inline: true,
		})
	} else {
		fields = append(fields, discordEmbedField{
			Name: "Players", Value: "\u2013", Inline: true,
		})
	}

	// Game day & time
	if running && status != nil && status.Day > 0 {
		dayNight := "\U0001F319 Night"
		if status.IsDay {
			dayNight = "\u2600\uFE0F Day"
		}
		gameTime := status.GameTime
		if gameTime == "" {
			gameTime = "\u2013"
		}
		fields = append(fields, discordEmbedField{
			Name: "Game", Value: fmt.Sprintf("Day %d  %s  (%s)", status.Day, dayNight, gameTime), Inline: false,
		})
	} else if running {
		fields = append(fields, discordEmbedField{
			Name: "Game", Value: "Loading...", Inline: false,
		})
	}

	// World
	if running && status != nil && status.World != "" {
		fields = append(fields, discordEmbedField{
			Name: "World", Value: status.World, Inline: true,
		})
	}

	// Last restart
	if running && uptime > 0 {
		restartTime := time.Now().Add(-uptime)
		fields = append(fields, discordEmbedField{
			Name: "Last Restart", Value: restartTime.Format("2006-01-02 15:04:05"), Inline: true,
		})
	}

	// Player list
	if running && len(players) > 0 {
		var names []string
		for _, p := range players {
			names = append(names, p.Name)
		}
		fields = append(fields, discordEmbedField{
			Name: "\U0001F465 Players", Value: strings.Join(names, ", "), Inline: false,
		})
	} else if running {
		fields = append(fields, discordEmbedField{
			Name: "\U0001F465 Players", Value: "No players online", Inline: false,
		})
	} else {
		fields = append(fields, discordEmbedField{
			Name: "\U0001F465 Players", Value: "Server is offline", Inline: false,
		})
	}

	// Mod lists from manifest
	var required, optional []string
	for _, m := range manifestMods {
		entry := m.Name
		if m.Version != "" {
			entry += " v" + m.Version
		}
		switch m.Anticheat {
		case "whitelist":
			required = append(required, entry)
		case "greylist":
			optional = append(optional, entry)
		}
	}
	if len(required) > 0 {
		fields = append(fields, discordEmbedField{
			Name: "\U0001F4CB Required Mods", Value: truncateModList(required), Inline: false,
		})
	}
	if len(optional) > 0 {
		fields = append(fields, discordEmbedField{
			Name: "\U0001F4CB Optional Mods", Value: truncateModList(optional), Inline: false,
		})
	}

	return discordEmbedMessage{
		Embeds: []discordEmbed{{
			Title:     "\u2694\uFE0F Server Status",
			Color:     color,
			Fields:    fields,
			Footer:    &discordEmbedFooter{Text: "Last updated"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	}
}

func truncateModList(entries []string) string {
	var b strings.Builder
	for i, entry := range entries {
		line := "• " + entry + "\n"
		if b.Len()+len(line) > maxFieldLen {
			fmt.Fprintf(&b, "… and %d more", len(entries)-i)
			break
		}
		b.WriteString(line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func createDiscordEmbed(webhookURL string, msg discordEmbedMessage) (string, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(webhookURL+"?wait=true", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result.ID, nil
}

// errMessageNotFound is returned when Discord 404s on an embed edit (message deleted).
var errMessageNotFound = fmt.Errorf("message not found")

func editDiscordEmbed(webhookURL string, messageID string, msg discordEmbedMessage) error {
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodPatch, webhookURL+"/messages/"+messageID, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("patch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return errMessageNotFound
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
