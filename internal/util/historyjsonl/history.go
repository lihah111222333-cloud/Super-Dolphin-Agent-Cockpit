// Package historyjsonl 从 provider 本地 JSONL 历史中恢复对话消息。
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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/sessionpaths"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

// ReadRequest 是读取 provider 历史所需的跨模块参数。
// RolloutPath 显式给出时优先使用；否则按 provider 的本地目录约定回退查找。
type ReadRequest struct {
	Provider         string // provider 名称，决定 JSONL 解析格式和默认发现路径。
	RolloutPath      string // 显式 JSONL 文件路径，通常来自已记录的 rollout 元数据。
	ThreadID         string // 平台内部 thread ID，作为 provider history 查找候选。
	ProviderThreadID string // provider 原生 thread/session ID，优先于内部 ID 匹配。
	SessionUUID      string // provider 会话 UUID，Claude/Codex 历史恢复会用到。
	CodexHome        string // Codex home 覆盖路径；为空时使用用户默认 home。
}

// errProviderHistoryNotFound 标记 provider 本地历史缺失，供上层转换成业务错误。
var errProviderHistoryNotFound = errors.New("persisted thread history not found")

// textItem 是 Codex/Claude JSONL 中文本 content item 的最小兼容结构。
type textItem struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// ReadProviderMessages 读取并解析 provider 本地历史中的用户和助手消息。
// 文件缺失、路径错误或扫描失败都会直接返回错误，不在这里静默降级。
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

// ReadProviderMessagesIfExists 在历史文件存在时读取消息，缺失时返回 ok=false。
// 非缺失错误仍会冒泡，避免调用方把损坏历史误判为无历史。
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

// ReadProviderMessagesOrError 将历史缺失替换成调用方传入的业务错误。
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

// IsMissingProviderHistory 判断错误是否表示 provider 历史不存在。
func IsMissingProviderHistory(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, errProviderHistoryNotFound)
}

// ExistingProviderPath 返回可读取的 provider 历史文件路径。
// 路径缺失会包裹成 errProviderHistoryNotFound，目录路径视为错误而非空历史。
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

// resolvePath 优先使用显式 RolloutPath，否则按 provider 目录约定发现历史文件。
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

// discoverPath 根据 provider 类型选择本地历史发现策略。
func discoverPath(provider string, req ReadRequest) string {
	switch provider {
	case "claude":
		return discoverClaudePath(req)
	default:
		return discoverCodexPath(req)
	}
}

// discoverCodexPath 在 Codex sessions 目录中按候选 ID 查找最新 rollout JSONL。
func discoverCodexPath(req ReadRequest) string {
	root := codexRoot(req.CodexHome)
	for _, id := range []string{
		req.ProviderThreadID,
		req.ThreadID,
		req.SessionUUID,
	} {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		pattern, err := sessionpaths.CodexRolloutGlob(root, id)
		if err != nil {
			continue
		}
		if path := latestExistingMatch(pattern); path != "" {
			return path
		}
	}
	return ""
}

// discoverClaudePath 用多个候选 ID 查找 Claude 历史文件。
// Claude CLI 的真实 session UUID 可能异步写入，启动早期绑定库里不一定已有该值。
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

// claudeRoot 返回 Claude home；环境变量缺失或用户 home 不可读时返回空字符串。
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

// codexRoot 返回 Codex home；请求覆盖为空时回退到用户默认目录。
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

// latestExistingMatch 从 glob 结果尾部向前找最新存在的非目录文件。
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

// parseLine 按 provider JSONL 结构解析单行历史，无法生成消息时返回 ok=false。
func parseLine(raw []byte, provider string) (dto.Message, bool) {
	if strings.EqualFold(strings.TrimSpace(provider), "claude") {
		return parseClaudeLine(raw)
	}
	return parseCodexLine(raw)
}

// parseCodexLine 解析 Codex rollout JSONL 的 response_item/message 记录。
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

// parseClaudeLine 解析 Claude CLI JSONL 的 message 记录。
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

// buildMessage 过滤非 user/assistant 角色并标准化用户消息内容。
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

// rolloutSystemNoiseTagPairs 列出 Codex rollout 开头需要剥离的系统上下文块。
var rolloutSystemNoiseTagPairs = []struct {
	open  string
	close string
}{
	{open: "<environment_context>", close: "</environment_context>"},
	{open: "<instructions>", close: "</instructions>"},
	{open: "<permissions instructions>", close: "</permissions instructions>"},
	{open: "<turn_aborted>", close: "</turn_aborted>"},
}

// normalizeHistoryUserContent 去掉 provider 历史前缀中的系统噪声。
func normalizeHistoryUserContent(content string) (string, bool) {
	content = stripLeadingSystemNoise(content)
	if content == "" {
		return "", false
	}
	return content, true
}

// stripLeadingSystemNoise 连续剥离用户消息开头的系统上下文块。
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

// stripOneLeadingSystemNoise 剥离一个已知系统块，无法识别时保持原文。
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

// stripTagBlock 剥离指定闭合标签之前的内容，缺少闭合标签时返回空串。
func stripTagBlock(text, closeTag string) string {
	if idx := strings.Index(strings.ToLower(text), closeTag); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeTag):], "\ufeff \t\r\n")
	}
	return ""
}

// stripAgentsMDBlock 剥离嵌入历史开头的 AGENTS.md 指令块。
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

// collectText 合并 provider content item 中可展示的文本片段。
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

// parseTime 解析 provider 时间戳；空值或无法解析时返回零值时间。
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
