package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

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

// decodeAttachmentHint 解码attachmenthint。
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

func ensureTurnAvailable(handle *turnHandle) error {
	if handle == nil {
		return nil
	}
	if err := ensureTurnOpen(handle); err != nil {
		return errors.New("claudecli: turn already running")
	}
	return nil
}

func ensureTransportReady(tr *transport) error {
	if tr == nil {
		return errors.New("claudecli: session transport is closed")
	}
	return nil
}

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

// decodeAssistantMessageBlock 解码assistant消息block。
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

// decodeUserMessageBlock 解码user消息block。
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

// toolResultContent 处理工具结果内容。
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

func messageDeltaEvent(data map[string]any, stream, delta string) (dto.RawProviderEvent, bool) {
	if strings.TrimSpace(delta) == "" {
		return dto.RawProviderEvent{}, false
	}
	data["stream"] = stream
	data["delta"] = delta
	return dto.RawProviderEvent{EventType: "assistant:message_delta", Data: data}, true
}

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

func appendFlagIfSet(args []string, flag, value string) []string {
	if value = strings.TrimSpace(value); value != "" {
		args = append(args, flag, value)
	}
	return args
}

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

// waitThreadReady 等待线程进入可用状态。
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

func threadReadyContextErr(err error) error {
	return fmt.Errorf("claudecli: waiting for real thread id: %w", err)
}

// ensureProcessAlive 确保进程alive。
func (t *transport) ensureProcessAlive() (int, error) {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return 0, nil
	}
	// Do not read cmd.ProcessState here: os/exec.Cmd.Wait writes that field,
	// and callers such as Running/signalProcess can race with the wait goroutine
	// under -race. The transport-owned done channel is the synchronization point;
	// if Wait has not closed it yet, returning the pid is safe and any late signal
	// will be normalized by the caller when the process is already gone.
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

func shouldKeepEmptyMessage(msg Message) bool {
	return len(msg.Metadata) > 0 && string(msg.Metadata) != "null"
}

func stripSystemNoise(text string) string {
	return trimInjectedClaudeLSPHint(trimInjectedClaudeSkillBlock(stripLeadingClaudeSystemNoise(text)))
}
