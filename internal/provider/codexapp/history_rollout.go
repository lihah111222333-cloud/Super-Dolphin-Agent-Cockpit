package codexapp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
)

type rolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type rolloutPayload struct {
	Type    string               `json:"type"`
	Role    string               `json:"role"`
	Content []rolloutContentItem `json:"content"`
}

type rolloutContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// readLocalRollout 读取localrollout。
func readLocalRollout(threadID, codexHome string, limit int) ([]Message, error) {
	path, err := findRolloutPath(threadID, codexHome)
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

// parseRolloutLine 解析rollout行。
func parseRolloutLine(raw []byte) (Message, bool) {
	var line rolloutLine
	if err := json.Unmarshal(raw, &line); err != nil || line.Type != "response_item" {
		return Message{}, false
	}
	var payload rolloutPayload
	if err := json.Unmarshal(line.Payload, &payload); err != nil || payload.Type != "message" {
		return Message{}, false
	}
	text := strings.TrimSpace(extractRolloutText(payload.Content))
	metadata := extractRolloutMetadata(payload.Role, payload.Content)
	if payload.Role == "user" {
		var ok bool
		text, ok = normalizeRolloutUserContent(text, metadata)
		if !ok {
			return Message{}, false
		}
	}
	if (payload.Role != "user" && payload.Role != "assistant") || !rolloutMessageHasContent(text, metadata) {
		return Message{}, false
	}
	return Message{
		Role:      payload.Role,
		Content:   text,
		Metadata:  metadata,
		Timestamp: line.Timestamp,
	}, true
}

func extractRolloutText(content []rolloutContentItem) string {
	var b strings.Builder
	for _, item := range content {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "text", "input_text", "output_text":
			b.WriteString(item.Text)
		}
	}
	return b.String()
}

func extractRolloutMetadata(role string, content []rolloutContentItem) json.RawMessage {
	if !strings.EqualFold(strings.TrimSpace(role), "user") {
		return nil
	}
	inputs := collectRolloutMetadata(content)
	if len(inputs) == 0 {
		return nil
	}
	return marshalRolloutMetadata(inputs)
}

func collectRolloutMetadata(content []rolloutContentItem) []map[string]any {
	inputs := make([]map[string]any, 0, len(content))
	for _, item := range content {
		input, _ := rolloutMetadataItem(item)
		if len(input) != 0 {
			inputs = append(inputs, input)
		}
	}
	return inputs
}

func rolloutMetadataItem(item rolloutContentItem) (map[string]any, bool) {
	kind := strings.ToLower(strings.TrimSpace(item.Type))
	if kind == "input_image" {
		imageURL := strings.TrimSpace(item.ImageURL)
		if imageURL == "" {
			return map[string]any{"type": "image"}, true
		}
		return map[string]any{"type": "image", "url": imageURL}, true
	}
	if strings.HasPrefix(kind, "input_") && kind != "input_text" {
		return map[string]any{"type": normalizeRolloutInputType(kind)}, true
	}
	return nil, false
}

func marshalRolloutMetadata(inputs []map[string]any) json.RawMessage {
	raw, err := json.Marshal(map[string]any{"input": inputs})
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

func rolloutMessageHasContent(text string, metadata json.RawMessage) bool {
	return strings.TrimSpace(text) != "" || len(metadata) > 0
}

func normalizeRolloutUserContent(text string, metadata json.RawMessage) (string, bool) {
	text = stripLeadingSystemNoise(text)
	text = trimInjectedLSPHint(trimInjectedSkillBlock(text))
	if !rolloutMessageHasContent(text, metadata) {
		return text, false
	}
	if strings.TrimSpace(text) != "" && isSystemNoiseText(text) {
		return text, false
	}
	return strings.TrimSpace(text), true
}

const rolloutSystemNoiseTrimLeftCutset = "\ufeff \t\r\n"

var rolloutSystemNoiseTagPairs = []struct {
	open  string
	close string
}{
	{open: "<environment_context>", close: "</environment_context>"},
	{open: "<instructions>", close: "</instructions>"},
	{open: "<permissions instructions>", close: "</permissions instructions>"},
	{open: "<turn_aborted>", close: "</turn_aborted>"},
}

func isSystemNoiseText(text string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(trimmed, "# agents.md") {
		return true
	}
	for _, pair := range rolloutSystemNoiseTagPairs {
		if strings.HasPrefix(trimmed, pair.open) {
			return true
		}
	}
	return false
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
	trimmed := strings.TrimLeft(text, rolloutSystemNoiseTrimLeftCutset)
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
		return strings.TrimLeft(text[idx+len(closeTag):], rolloutSystemNoiseTrimLeftCutset)
	}
	return ""
}

// stripAgentsMDBlock 处理strip代理mdblock。
func stripAgentsMDBlock(text string) string {
	const closeInstructions = "</instructions>"
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, closeInstructions); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeInstructions):], rolloutSystemNoiseTrimLeftCutset)
	}
	idx, width := strings.Index(text, "\n\n"), 2
	if crlf := strings.Index(text, "\r\n\r\n"); idx < 0 || (crlf >= 0 && crlf < idx) {
		idx, width = crlf, 4
	}
	if idx < 0 {
		return ""
	}
	return strings.TrimLeft(text[idx+width:], rolloutSystemNoiseTrimLeftCutset)
}

func trimInjectedLSPHint(text string) string {
	if idx := strings.Index(text, "\n已注入"); idx >= 0 {
		return text[:idx]
	}
	return text
}

// trimInjectedSkillBlock 委托给共享包。P20 Phase 3 统一两家 provider 的识别逻辑，
// 日后新增格式（如 Phase 4 的 [skill:name::mode@v1]）只需在 rollout_markers.go 中升级。
func trimInjectedSkillBlock(text string) string {
	return providershared.TrimInjectedSkillBlocks(text)
}

func normalizeRolloutInputType(kind string) string {
	kind = strings.TrimSpace(strings.TrimPrefix(kind, "input_"))
	if kind == "" {
		return "unknown"
	}
	return kind
}

// findRolloutPath locates the newest rollout jsonl for threadID under
// the given codex home. An empty codexHome falls back to ~/.codex so
// single-provider deployments keep working unchanged; multi-provider
// callers (see P21 Track B) pass the canonicalized codexHome that was
// persisted with the thread's binding, which is the only way to find
// rollout files written by a non-default codex instance.
func findRolloutPath(threadID, codexHome string) (string, error) {
	root, err := resolveRolloutRoot(codexHome)
	if err != nil {
		return "", err
	}
	pattern := filepath.Join(root, "sessions", "*", "*", "*", "rollout-*-"+strings.TrimSpace(threadID)+".jsonl")
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

// resolveRolloutRoot picks the directory whose sessions/ subtree is
// searched. A non-empty codexHome wins verbatim; it is expected to be
// a canonicalized absolute path already (see providershared
// .CanonicalizeCodexHome). Empty codexHome is a legacy-only fallback
// and now requires an explicit opt-in env flag.
func resolveRolloutRoot(codexHome string) (string, error) {
	if trimmed := strings.TrimSpace(codexHome); trimmed != "" {
		return trimmed, nil
	}
	if os.Getenv("CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME") != "1" {
		return "", fmt.Errorf("codex home required (set CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME=1 to allow ~/.codex fallback)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func trimMessages(messages []Message, limit int) []Message {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return append([]Message(nil), messages[len(messages)-limit:]...)
}
