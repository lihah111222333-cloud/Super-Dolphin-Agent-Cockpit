package historyjsonl

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type ReadRequest struct {
	Provider         string
	RolloutPath      string
	ThreadID         string
	ProviderThreadID string
	SessionUUID      string
	CodexHome        string
}

type textItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func ReadProviderMessages(req ReadRequest) ([]dto.Message, error) {
	path, provider, err := resolvePath(req)
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
	out := make([]dto.Message, 0, 32)
	for scanner.Scan() {
		msg, ok := parseLine(scanner.Bytes(), provider)
		if ok {
			out = append(out, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func ExistingProviderPath(req ReadRequest) (string, error) {
	path, _, err := resolvePath(req)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("persisted thread history not found: %w", err)
	}
	if info.IsDir() {
		return "", errors.New("persisted thread history path is a directory")
	}
	return path, nil
}

func resolvePath(req ReadRequest) (string, string, error) {
	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if path := strings.TrimSpace(req.RolloutPath); path != "" {
		return path, provider, nil
	}
	path := discoverPath(provider, req)
	if path == "" {
		return "", provider, errors.New("persisted thread history not found")
	}
	return path, provider, nil
}

func discoverPath(provider string, req ReadRequest) string {
	switch provider {
	case "claude":
		return discoverClaudePath(req)
	default:
		return latestExistingMatch(filepath.Join(codexRoot(req.CodexHome), "sessions", "*", "*", "*", "rollout-*-"+historyID(req, false)+".jsonl"))
	}
}

// discoverClaudePath tries all candidate IDs to find the Claude history file.
// The real session UUID is assigned asynchronously by Claude CLI (via system:init)
// and may not be persisted in the binding DB at startup time.
func discoverClaudePath(req ReadRequest) string {
	root := filepath.Join(claudeRoot(), "projects", "*")
	for _, id := range []string{
		req.SessionUUID,
		req.ProviderThreadID,
		req.ThreadID,
	} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if path := latestExistingMatch(filepath.Join(root, id+".jsonl")); path != "" {
			return path
		}
	}
	return ""
}

func historyID(req ReadRequest, preferSession bool) string {
	if preferSession {
		return shared.FirstNonEmpty(req.SessionUUID, req.ProviderThreadID, req.ThreadID)
	}
	return shared.FirstNonEmpty(req.ProviderThreadID, req.ThreadID)
}

func claudeRoot() string {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude")
}

func codexRoot(raw string) string {
	if root := strings.TrimSpace(raw); root != "" {
		return root
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func latestExistingMatch(pattern string) string {
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return ""
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if info, err := os.Stat(matches[i]); err == nil && !info.IsDir() {
			return matches[i]
		}
	}
	return ""
}

func parseLine(raw []byte, provider string) (dto.Message, bool) {
	if strings.EqualFold(strings.TrimSpace(provider), "claude") {
		return parseClaudeLine(raw)
	}
	return parseCodexLine(raw)
}

func parseCodexLine(raw []byte) (dto.Message, bool) {
	var line struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &line); err != nil || line.Type != "response_item" {
		return dto.Message{}, false
	}
	var payload struct {
		Type    string     `json:"type"`
		Role    string     `json:"role"`
		Content []textItem `json:"content"`
	}
	if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Type != "message" {
		return dto.Message{}, false
	}
	return buildMessage(payload.Role, collectText(payload.Content), line.Timestamp)
}

func parseClaudeLine(raw []byte) (dto.Message, bool) {
	var line struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			Role    string     `json:"role"`
			Content []textItem `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &line); err != nil {
		return dto.Message{}, false
	}
	return buildMessage(shared.FirstNonEmpty(line.Message.Role, line.Type), collectText(line.Message.Content), line.Timestamp)
}

func buildMessage(role, content, rawTime string) (dto.Message, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "user" && role != "assistant" {
		return dto.Message{}, false
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return dto.Message{}, false
	}
	return dto.Message{Role: role, Content: content, Timestamp: parseTime(rawTime)}, true
}

func collectText(items []textItem) string {
	var builder strings.Builder
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "text", "input_text", "output_text":
			builder.WriteString(item.Text)
		}
	}
	return builder.String()
}

func parseTime(raw string) time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
