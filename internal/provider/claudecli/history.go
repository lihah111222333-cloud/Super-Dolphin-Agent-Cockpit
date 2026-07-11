package claudecli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/historyjsonl"
)

type historyBackend struct {
	sessionDir string
}

// ReadHistory 从 Claude CLI 的 JSONL 会话文件读取完整消息历史。
// 新会话可能还没有落盘文件，此时返回空结果而不是把“未产生历史”视为错误。
func (h *historyBackend) ReadHistory(ctx context.Context, threadID string) ([]Message, error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return nil, err
	}
	path, err := h.sessionPath(threadID)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil // no history yet
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

// ReadMessagesPage 按 JSONL 游标分页读取 Claude CLI 历史。
// 路径不存在时保持空页语义，真正的文件读取或解析错误才向上返回。
func (h *historyBackend) ReadMessagesPage(ctx context.Context, threadID string, req dto.MessagePageRequest) (historyjsonl.JSONLPageResult[Message], error) {
	if err := shared.CheckCtx(ctx); err != nil {
		return historyjsonl.JSONLPageResult[Message]{}, err
	}
	path, err := h.sessionPath(threadID)
	if err != nil {
		return historyjsonl.JSONLPageResult[Message]{}, err
	}
	if path == "" {
		return historyjsonl.JSONLPageResult[Message]{}, nil
	}
	page, err := historyjsonl.ReadJSONLPage(path, req.Limit, req.Before, parseHistoryLine)
	if err != nil {
		return historyjsonl.JSONLPageResult[Message]{}, fmt.Errorf("read claude history page: %w", err)
	}
	return page, nil
}

// sessionPath 在 Claude projects 目录下定位指定 thread 的 JSONL 文件。
// Claude 会按项目目录分散保存历史，这里从 glob 结果尾部开始挑最新的真实文件。
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
	// No history file yet — normal for new sessions.
	return "", nil
}

func (h *historyBackend) rootDir() (string, error) {
	if dir := strings.TrimSpace(h.sessionDir); dir != "" {
		return dir, nil
	}
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
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

// parseHistoryLine 把 Claude CLI 的一行 JSONL 转成统一历史消息。
// 非 user/assistant 事件会被丢弃，图片块会尽量恢复成前端可预览的 metadata。
func parseHistoryLine(raw []byte) (Message, bool) {
	var line historyLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return Message{}, false
	}
	role := strings.ToLower(strings.TrimSpace(shared.FirstNonEmpty(line.Message.Role, line.Type)))
	if role != "user" && role != "assistant" {
		return Message{}, false
	}
	text := extractHistoryText(line.Message.Content)
	metadata := json.RawMessage(nil)
	if role == "user" {
		text, metadata = normalizeHistoryUserContent(text)
		// Vision turns persist images as native content blocks rather than the
		// legacy `[Image: path]` text hint, so when the text-hint parser finds
		// nothing we fall back to extracting metadata from the image blocks.
		if len(metadata) == 0 {
			metadata = extractImageContentBlocksMetadata(line.Message.Content)
		}
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

// extractImageContentBlocksMetadata 从 Claude 原生 image content block 还原 InputItem metadata。
// Claude 历史不保留本地文件路径，只保留 inline base64，因此这里生成 data: URL 供前端直接预览。
func extractImageContentBlocksMetadata(items []historyContentItem) json.RawMessage {
	inputs := make([]map[string]any, 0)
	for _, item := range items {
		if !strings.EqualFold(strings.TrimSpace(item.Type), "image") || item.Source == nil {
			continue
		}
		record := historyImageInputFromSource(item.Source)
		if record == nil {
			continue
		}
		inputs = append(inputs, record)
	}
	if len(inputs) == 0 {
		return nil
	}
	raw, err := json.Marshal(map[string]any{"input": inputs})
	if err != nil {
		return nil
	}
	return raw
}

// historyImageInputFromSource 将 Claude image source 转成前端 InputItem 形状。
// base64 图片会补 sha256，后续去重占位符可用它找回原始预览。
func historyImageInputFromSource(source *historyImageSource) map[string]any {
	if source == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(source.Type)) {
	case "base64":
		mediaType := strings.TrimSpace(source.MediaType)
		data := strings.TrimSpace(source.Data)
		if mediaType == "" || data == "" {
			return nil
		}
		record := map[string]any{
			"type": "image",
			"url":  "data:" + mediaType + ";base64," + data,
		}
		if h := sha256OfBase64Data(data); h != "" {
			// sha256 lets the frontend correlate this image with `[image
			// previously attached … sha256:abc…]` placeholders that follow on
			// later turns and re-render the original preview in their bubble.
			record["sha256"] = h
		}
		return record
	case "url":
		url := strings.TrimSpace(source.URL)
		if url == "" {
			return nil
		}
		return map[string]any{
			"type": "image",
			"url":  url,
		}
	}
	return nil
}

func sha256OfBase64Data(data string) string {
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil || len(decoded) == 0 {
		return ""
	}
	sum := sha256.Sum256(decoded)
	return hex.EncodeToString(sum[:])
}

// extractInjectedAttachmentMetadata 从注入的文件提示块还原附件 metadata。
// 只有识别到专用 header 才消费文本，避免普通用户内容被误当作结构化附件。
func extractInjectedAttachmentMetadata(text string) (string, json.RawMessage) {
	trimmed := strings.TrimLeft(text, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, injectedFileHintsHeader) {
		// Claude 目前把文件提示持久化为文本；若未来出现非文本历史项，这里再直接恢复结构化 metadata。
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
		input, ok := decodeAttachmentHint(line)
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
