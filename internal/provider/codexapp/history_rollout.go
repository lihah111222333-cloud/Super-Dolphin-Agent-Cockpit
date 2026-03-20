package codexapp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutPayload struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	} `json:"content"`
}

func readLocalRollout(threadID string, limit int) ([]Message, error) {
	path, err := findRolloutPath(threadID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	messages := make([]Message, 0, 32)
	for scanner.Scan() {
		if msg, ok := parseRolloutLine(scanner.Bytes()); ok {
			messages = append(messages, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return trimMessages(messages, limit), nil
}

func parseRolloutLine(raw []byte) (Message, bool) {
	var line rolloutLine
	if err := json.Unmarshal(raw, &line); err != nil || line.Type != "response_item" {
		return Message{}, false
	}
	var payload rolloutPayload
	if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Type != "message" {
		return Message{}, false
	}
	var b strings.Builder
	for _, item := range payload.Content {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "text", "input_text", "output_text":
			b.WriteString(item.Text)
		}
	}
	text := strings.TrimSpace(b.String())
	if text == "" || (payload.Role != "user" && payload.Role != "assistant") {
		return Message{}, false
	}
	return Message{Role: payload.Role, Content: text, Timestamp: line.Timestamp}, true
}

func findRolloutPath(threadID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*-"+strings.TrimSpace(threadID)+".jsonl")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("codexapp: rollout not found for %s", threadID)
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

func trimMessages(messages []Message, limit int) []Message {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return append([]Message(nil), messages[len(messages)-limit:]...)
}
