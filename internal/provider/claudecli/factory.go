package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// encodeAttachmentHint 将文件或图片输入编码成可写入 Claude 文本流的提示行。
func encodeAttachmentHint(input dto.InputItem) string {
	target := shared.FirstNonEmpty(input.Path, input.URL)
	if target == "" {
		return ""
	}
	label := "File"
	if strings.EqualFold(strings.TrimSpace(input.Type), "image") {
		label = "Image"
	}
	if name := strings.TrimSpace(input.Name); name != "" && name != target {
		target = name + " -> " + target
	}
	return "[" + label + ": " + target + "]"
}

// decodeAttachmentHint 从 Claude 文本流中还原附件提示行。
func decodeAttachmentHint(line string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	prefix, inputType, targetKey := "", "", ""
	switch {
	case strings.HasPrefix(lower, "[image:"):
		prefix, inputType, targetKey = "[image:", "image", "url"
	case strings.HasPrefix(lower, "[file:"):
		prefix, inputType, targetKey = "[file:", "mention", "path"
	default:
		return nil, false
	}
	if !strings.HasSuffix(trimmed, "]") {
		return nil, false
	}
	value := strings.TrimSpace(trimmed[len(prefix) : len(trimmed)-1])
	if value == "" {
		return nil, false
	}
	name, target := splitAttachmentHintValue(value)
	item := map[string]any{"type": inputType, targetKey: target}
	if name != "" {
		item["name"] = name
	}
	return item, true
}

// splitAttachmentHintValue 拆分附件展示名和真实目标地址。
func splitAttachmentHintValue(value string) (string, string) {
	parts := strings.SplitN(value, " -> ", 2)
	if len(parts) != 2 {
		return "", value
	}
	name := strings.TrimSpace(parts[0])
	target := strings.TrimSpace(parts[1])
	if name == "" || target == "" {
		return "", value
	}
	return name, target
}

// finishTurnWithError 结束当前 turn 并发布失败的 turn:complete 事件。
func (s *session) finishTurnWithError(handle *turnHandle, err error) {
	if handle == nil {
		return
	}
	if err == nil {
		err = errors.New("claudecli: turn failed")
	}
	turnID := currentTurnID(handle)
	handle.finish(err)
	s.dispatch(s.turnRawEvent("turn:complete", turnID, map[string]any{
		"success": false,
		"error":   err.Error(),
	}))
}

// takeActiveTurnLocked 在持锁状态下取走 active turn，并取消尚未执行的 retry。
func (s *session) takeActiveTurnLocked() *turnHandle {
	if s == nil {
		return nil
	}
	handle := s.activeTurn
	s.activeTurn = nil
	if retry := s.pendingRetry; retry != nil {
		if retry.cancel != nil {
			retry.cancel()
		}
		s.pendingRetry = nil
	}
	return handle
}

// ensureTurnAvailable 确认当前 turn handle 没有被其他请求占用。
func ensureTurnAvailable(handle *turnHandle) error {
	if handle == nil {
		return nil
	}
	if err := ensureTurnOpen(handle); err != nil {
		return errors.New("claudecli: turn already running")
	}
	return nil
}

// ensureTransportReady 确认 session transport 仍可发送请求。
func ensureTransportReady(tr *transport) error {
	if tr == nil {
		return errors.New("claudecli: session transport is closed")
	}
	return nil
}

// decodeMessageEvents 将 Claude stream message 里的 content block 展开为内部 RawProviderEvent。
func decodeMessageEvents(raw streamEvent, base rawBase, role string) ([]dto.RawProviderEvent, error) {
	var msg struct {
		Content []json.RawMessage `json:"content"`
	}
	if len(raw.Message) > 0 {
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return nil, err
		}
	}
	out := make([]dto.RawProviderEvent, 0, len(msg.Content))
	for _, rawBlock := range msg.Content {
		data := buildEventData(base, raw.SessionID, raw.Timestamp, nil)
		events, err := decodeMessageBlock(role, rawBlock, data)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	return out, nil
}

// decodeMessageBlock 根据 role 分发 assistant/user content block 的解码逻辑。
func decodeMessageBlock(role string, rawBlock json.RawMessage, data map[string]any) ([]dto.RawProviderEvent, error) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant":
		return decodeAssistantMessageBlock(rawBlock, data)
	case "user":
		return decodeUserMessageBlock(rawBlock, data)
	default:
		pkglogger.Get().Warn("claudecli: unknown message block role", "role", role)
		return nil, nil
	}
}

// decodeAssistantMessageBlock 将 assistant text/thinking/tool_use block 转成增量或工具开始事件。
func decodeAssistantMessageBlock(rawBlock json.RawMessage, data map[string]any) ([]dto.RawProviderEvent, error) {
	var block contentBlock
	if err := json.Unmarshal(rawBlock, &block); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(block.Type) {
	case "text":
		if event, ok := messageDeltaEvent(data, "message", block.Text); ok {
			return []dto.RawProviderEvent{event}, nil
		}
	case "thinking":
		if event, ok := messageDeltaEvent(data, "reasoning", block.Thinking); ok {
			return []dto.RawProviderEvent{event}, nil
		}
	case "tool_use":
		data["call_id"] = strings.TrimSpace(block.ID)
		data["tool_name"] = strings.TrimSpace(block.Name)
		data["arguments_preview"] = strings.TrimSpace(string(block.Input))
		return []dto.RawProviderEvent{{EventType: "tool:use_begin", Data: data}}, nil
	}
	return nil, nil
}

// decodeUserMessageBlock 将 user tool_result block 转成工具结束事件。
func decodeUserMessageBlock(rawBlock json.RawMessage, data map[string]any) ([]dto.RawProviderEvent, error) {
	var block map[string]any
	if err := json.Unmarshal(rawBlock, &block); err != nil {
		return nil, err
	}
	if dataString(block, "type") != "tool_result" {
		return nil, nil
	}
	success := true
	if flag, ok := block["is_error"].(bool); ok {
		success = !flag
	}
	data["call_id"] = dataString(block, "tool_use_id")
	data["tool_name"] = dataString(block, "tool_name")
	data["success"] = success
	if content := toolResultContent(block["content"]); content != "" {
		if success {
			data["result"] = content
		} else {
			data["error"] = content
		}
	}
	return []dto.RawProviderEvent{{EventType: "tool:use_end", Data: data}}, nil
}

// toolResultContent 从 Claude tool_result content 中提取可展示文本。
func toolResultContent(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if content := toolResultContent(item); content != "" {
				parts = append(parts, content)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n"))
	case map[string]any:
		if text := dataString(value, "text", "content"); text != "" {
			return text
		}
		if marshaled, err := json.Marshal(value); err == nil {
			return strings.TrimSpace(string(marshaled))
		}
	}
	if raw == nil {
		return ""
	}
	marshaled, err := json.Marshal(raw)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(marshaled))
}

// messageDeltaEvent 构造非空 assistant 输出增量事件。
func messageDeltaEvent(data map[string]any, stream, delta string) (dto.RawProviderEvent, bool) {
	if strings.TrimSpace(delta) == "" {
		return dto.RawProviderEvent{}, false
	}
	data["stream"] = stream
	data["delta"] = delta
	return dto.RawProviderEvent{EventType: "assistant:message_delta", Data: data}, true
}

// buildEventData 从 base 字段、sessionID、timestamp 和 extras 合成事件 data。
func buildEventData(base rawBase, sessionID, timestamp string, extras map[string]any) map[string]any {
	threadID := strings.TrimSpace(base.ThreadID)
	data := map[string]any{
		"agent_id":   strings.TrimSpace(base.AgentID),
		"thread_id":  threadID,
		"session_id": shared.FirstNonEmpty(sessionID, threadID),
		"turn_id":    strings.TrimSpace(base.TurnID),
	}
	if timestamp = strings.TrimSpace(timestamp); timestamp != "" {
		data["timestamp"] = timestamp
	}
	for key, value := range extras {
		data[key] = value
	}
	return data
}

// appendFlagIfSet 在值非空时追加 CLI flag 和参数。
func appendFlagIfSet(args []string, flag, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		args = append(args, flag, value)
	}
	return args
}

// cleanupOnError 在 err 非空时按顺序执行清理函数，并返回原始错误。
func cleanupOnError(err error, cleanups ...func()) error {
	if err == nil {
		return nil
	}
	for _, cleanup := range cleanups {
		if cleanup != nil {
			cleanup()
		}
	}
	return err
}

// waitThreadReady 等待 thread ready 信号；transport 已退出时返回进程状态错误。
func waitThreadReady(ctx context.Context, ready <-chan struct{}, tr *transport) error {
	if ready == nil {
		return nil
	}
	select {
	case <-ready:
		return nil
	default:
	}
	waitCtx, cancel := withThreadIDTimeout(ctx)
	defer cancel()
	if tr == nil {
		select {
		case <-ready:
			return nil
		case <-waitCtx.Done():
			return threadReadyContextErr(waitCtx.Err())
		}
	}
	return waitForThreadReadyOrExit(waitCtx, ready, tr)
}

// threadReadyContextErr 将等待 thread ready 的 context 错误统一为可读错误。
func threadReadyContextErr(err error) error {
	return fmt.Errorf("claudecli: waiting for real thread id: %w", err)
}

// ensureProcessAlive 检查 transport 进程是否仍在运行，并返回 pid。
func (t *transport) ensureProcessAlive() (int, error) {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return 0, nil
	}
	// 不读取 cmd.ProcessState：os/exec.Cmd.Wait 会写该字段，Running/signalProcess
	// 可能与 wait goroutine 在 -race 下竞争。transport 自有 done channel 是同步点；
	// done 未关闭时返回 pid 是安全的，进程刚退出的后续 signal 会由调用方归一化处理。
	select {
	case <-t.done:
		return 0, nil
	default:
	}
	pid := t.cmd.Process.Pid
	if pid <= 0 {
		return 0, errors.New("invalid claude pid")
	}
	return pid, nil
}

// shouldKeepEmptyMessage 判断空消息是否仍需保留；带 metadata 的消息不能丢弃。
func shouldKeepEmptyMessage(msg Message) bool {
	return len(msg.Metadata) > 0 && string(msg.Metadata) != "null"
}

// stripSystemNoise 去掉 Claude CLI 输出前缀中注入的系统提示噪声。
func stripSystemNoise(text string) string {
	return trimInjectedClaudeLSPHint(trimInjectedClaudeSkillBlock(stripLeadingClaudeSystemNoise(text)))
}
