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
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type ReadRequest struct {
	Provider         string
	RolloutPath      string
	ThreadID         string
	ProviderThreadID string
	SessionUUID      string
	CodexHome        string
}

var errProviderHistoryNotFound = errors.New("persisted thread history not found")

type textItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ReadProviderMessages 读取provider消息。
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

// ReadProviderMessagesIfExists 读取provider消息ifexists。
func ReadProviderMessagesIfExists(req ReadRequest) ([]dto.Message, bool, error) {
	if _, err := ExistingProviderPath(req); err == nil {
		messages, readErr := ReadProviderMessages(req)
		if readErr != nil {
			return nil, false, readErr
		}
		return messages, true, nil
	} else if !IsMissingProviderHistory(err) {
		return nil, false, err
	}
	return nil, false, nil
}

// ReadProviderMessagesOrError 读取provider消息错误。
func ReadProviderMessagesOrError(req ReadRequest, missingErr error) ([]dto.Message, error) {
	messages, ok, err := ReadProviderMessagesIfExists(req)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, missingErr
	}
	return messages, nil
}

// IsMissingProviderHistory 判断missingproviderhistory是否可用。
func IsMissingProviderHistory(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, errProviderHistoryNotFound)
}

// ExistingProviderPath 处理existingprovider路径。
func ExistingProviderPath(req ReadRequest) (string, error) {
	path, _, err := resolvePath(req)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %w", errProviderHistoryNotFound, err)
		}
		return "", fmt.Errorf("stat persisted thread history: %w", err)
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
		return "", provider, errProviderHistoryNotFound
	}
	return path, provider, nil
}

func discoverPath(provider string, req ReadRequest) string {
	switch provider {
	case "claude":
		return discoverClaudePath(req)
	default:
		return discoverCodexPath(req)
	}
}

func discoverCodexPath(req ReadRequest) string {
	root := filepath.Join(codexRoot(req.CodexHome), "sessions", "*", "*", "*")
	for _, id := range []string{
		req.ProviderThreadID,
		req.ThreadID,
		req.SessionUUID,
	} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if path := latestExistingMatch(filepath.Join(root, "rollout-*-"+id+".jsonl")); path != "" {
			return path
		}
	}
	return ""
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

// buildMessage 构建消息。
func buildMessage(role, content, rawTime string) (dto.Message, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	if role != "user" && role != "assistant" {
		return dto.Message{}, false
	}
	content = strings.TrimSpace(content)
	if role == "user" {
		var ok bool
		content, ok = normalizeHistoryUserContent(content)
		if !ok {
			return dto.Message{}, false
		}
	}
	if content == "" {
		return dto.Message{}, false
	}
	return dto.Message{Role: role, Content: content, Timestamp: parseTime(rawTime)}, true
}

var rolloutSystemNoiseTagPairs = []struct {
	open  string
	close string
}{
	{open: "<environment_context>", close: "</environment_context>"},
	{open: "<instructions>", close: "</instructions>"},
	{open: "<permissions instructions>", close: "</permissions instructions>"},
	{open: "<turn_aborted>", close: "</turn_aborted>"},
}

func normalizeHistoryUserContent(content string) (string, bool) {
	content = stripLeadingSystemNoise(content)
	if content == "" {
		return "", false
	}
	return content, true
}

func stripLeadingSystemNoise(text string) string {
	for current := text; ; {
		next, stripped := stripOneLeadingSystemNoise(current)
		if !stripped {
			return current
		}
		current = next
		if strings.TrimSpace(current) == "" {
			return ""
		}
	}
}

func stripOneLeadingSystemNoise(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, "\ufeff \t\r\n")
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "# agents.md") {
		return stripAgentsMDBlock(trimmed), true
	}
	for _, pair := range rolloutSystemNoiseTagPairs {
		if strings.HasPrefix(lower, pair.open) {
			return stripTagBlock(trimmed, pair.close), true
		}
	}
	return text, false
}

func stripTagBlock(text, closeTag string) string {
	if idx := strings.Index(strings.ToLower(text), closeTag); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeTag):], "\ufeff \t\r\n")
	}
	return ""
}

// stripAgentsMDBlock 处理strip代理mdblock。
func stripAgentsMDBlock(text string) string {
	const closeInstructions = "</instructions>"
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, closeInstructions); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeInstructions):], "\ufeff \t\r\n")
	}
	idx, width := strings.Index(text, "\n\n"), 2
	if crlf := strings.Index(text, "\r\n\r\n"); idx < 0 || (crlf >= 0 && crlf < idx) {
		idx, width = crlf, 4
	}
	if idx < 0 {
		return ""
	}
	return strings.TrimLeft(text[idx+width:], "\ufeff \t\r\n")
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
