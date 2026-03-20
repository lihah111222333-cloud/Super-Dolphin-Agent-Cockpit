package claudecli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type historyBackend struct {
	sessionDir string
}

func (h *historyBackend) ReadHistory(ctx context.Context, threadID string) ([]Message, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := h.sessionPath(threadID)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open claude history: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 100*1024*1024)
	var out []Message
	for scanner.Scan() {
		msg, ok := parseHistoryLine(scanner.Bytes())
		if ok {
			out = append(out, msg)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan claude history: %w", err)
	}
	return out, nil
}

func (h *historyBackend) sessionPath(threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errors.New("claudecli: empty thread id")
	}
	root, err := h.rootDir()
	if err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(root, "projects", "*", threadID+".jsonl"))
	if err != nil {
		return "", fmt.Errorf("glob claude history: %w", err)
	}
	for i := len(matches) - 1; i >= 0; i-- {
		if info, err := os.Stat(matches[i]); err == nil && !info.IsDir() {
			return matches[i], nil
		}
	}
	return "", fmt.Errorf("claudecli: history not found for %s", threadID)
}

func (h *historyBackend) rootDir() (string, error) {
	if dir := strings.TrimSpace(h.sessionDir); dir != "" {
		return dir, nil
	}
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_HOME")); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

func parseHistoryLine(raw []byte) (Message, bool) {
	var line historyLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return Message{}, false
	}
	role := strings.ToLower(strings.TrimSpace(firstNonEmpty(line.Message.Role, line.Type)))
	if role != "user" && role != "assistant" {
		return Message{}, false
	}
	text := extractHistoryText(line.Message.Content)
	metadata := json.RawMessage(nil)
	if role == "user" {
		text, metadata = normalizeHistoryUserContent(text)
	}
	if strings.TrimSpace(text) == "" && len(metadata) == 0 {
		return Message{}, false
	}
	return Message{
		Role:      role,
		Content:   text,
		Metadata:  metadata,
		Timestamp: line.Timestamp,
	}, true
}

func extractHistoryText(items []historyContentItem) string {
	var builder strings.Builder
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Type), "text") {
			builder.WriteString(item.Text)
		}
	}
	return builder.String()
}

func normalizeHistoryUserContent(text string) (string, json.RawMessage) {
	text, metadata := extractInjectedAttachmentMetadata(text)
	return strings.TrimSpace(text), metadata
}

func extractInjectedAttachmentMetadata(text string) (string, json.RawMessage) {
	trimmed := strings.TrimLeft(text, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, injectedFileHintsHeader) {
		// TODO(P7): recover structured attachment metadata directly if Claude history adds non-text items.
		return text, nil
	}
	remainder := strings.TrimLeft(trimmed[len(injectedFileHintsHeader):], "\r\n")
	lines := strings.Split(remainder, "\n")
	inputs := make([]map[string]any, 0, len(lines))
	consumed := 0
	for consumed < len(lines) {
		line := strings.TrimSpace(lines[consumed])
		if line == "" {
			consumed++
			break
		}
		input, ok := parseInjectedAttachmentHintLine(line)
		if !ok {
			break
		}
		inputs = append(inputs, input)
		consumed++
	}
	if len(inputs) == 0 {
		return text, nil
	}
	cleaned := strings.TrimLeft(strings.Join(lines[consumed:], "\n"), "\r\n")
	raw, err := json.Marshal(map[string]any{"input": inputs})
	if err != nil {
		return cleaned, nil
	}
	return cleaned, raw
}

func parseInjectedAttachmentHintLine(line string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "[image:"):
		value, ok := parseInjectedAttachmentHintValue(trimmed, "[image:")
		if !ok {
			return nil, false
		}
		return map[string]any{"type": "image", "url": value}, true
	case strings.HasPrefix(lower, "[file:"):
		value, ok := parseInjectedAttachmentHintValue(trimmed, "[file:")
		if !ok {
			return nil, false
		}
		return map[string]any{"type": "mention", "path": value}, true
	default:
		return nil, false
	}
}

func parseInjectedAttachmentHintValue(line, prefix string) (string, bool) {
	if !strings.HasSuffix(line, "]") {
		return "", false
	}
	value := strings.TrimSpace(line[len(prefix) : len(line)-1])
	if value == "" {
		return "", false
	}
	return value, true
}
