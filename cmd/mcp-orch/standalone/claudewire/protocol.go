// Package claudewire 定义官方 Claude CLI stream-json 协议边界。
package claudewire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Event 保留独立执行器消费的 CLI 事件字段；正文不能被当成控制命令。
type Event struct {
	// SystemMessage只保存system事件的文本元数据，不参与result或native acknowledgement。
	SystemMessage   string   `json:"-"`
	IsReplay        bool     `json:"isReplay"`
	Type            string   `json:"type"`
	Subtype         string   `json:"subtype"`
	SessionID       string   `json:"session_id"`
	UUID            string   `json:"uuid"`
	ParentToolUseID *string  `json:"parent_tool_use_id"`
	IsError         bool     `json:"is_error"`
	Result          string   `json:"result"`
	Tools           []string `json:"tools"`
	MCPServers      []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"mcp_servers"`
	Message struct {
		Content []Content `json:"content"`
	} `json:"message"`
}

// IsSystemInit 识别首条输入后的 provider 初始化帧，并要求根 session 身份一致。
func (e Event) IsSystemInit(expectedSession string) bool {
	return e.Type == "system" && e.Subtype == "init" && e.SessionID == expectedSession && e.ParentToolUseID == nil
}

// Content 是文本/tool块的最小保真描述，非文本块不冒充 assistant 文本。
type Content struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type userMessage struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	UUID      string `json:"uuid"`
	Message   struct {
		Role    string    `json:"role"`
		Content []Content `json:"content"`
	} `json:"message"`
}

// EncodeUser 明确携带 session 和请求 UUID，拒绝空正文及隐式会话。
func EncodeUser(sessionID, requestID, text string) ([]byte, error) {
	if sessionID == "" || requestID == "" || text == "" {
		return nil, errors.New("Claude message requires session, request id and text")
	}
	message := userMessage{Type: "user", SessionID: sessionID, UUID: requestID}
	message.Message.Role = "user"
	message.Message.Content = []Content{{Type: "text", Text: text}}
	return json.Marshal(message)
}

// DecodeEvent 只接受完整 JSON object，拒绝 stdout 日志或缺失事件类型。
func DecodeEvent(raw []byte) (Event, error) {
	var event Event
	type eventFields Event
	wire := struct {
		*eventFields
		Message json.RawMessage `json:"message"`
	}{eventFields: (*eventFields)(&event)}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return Event{}, safeEventDecodeError(raw, err)
	}
	if err := decodeEventMessage(&event, wire.Message); err != nil {
		return Event{}, safeEventDecodeError(raw, err)
	}
	if event.Type == "" {
		return Event{}, errors.New("Claude event type is required")
	}
	return event, nil
}

// ResultText 只接收根会话的明确成功结果，子 agent 文本不覆盖根报告。
func (e Event) ResultText(expectedSession string) (string, error) {
	if e.Type != "result" || e.SessionID != expectedSession || e.ParentToolUseID != nil {
		return "", errors.New("Claude result identity mismatch")
	}
	if e.IsError || e.Subtype != "success" {
		return "", errors.New("Claude CLI returned a failed result")
	}
	return e.Result, nil
}

// safeEventDecodeError 只报告静态字段路径和JSON类型，禁止将解析正文或数值嵌入日志。
func safeEventDecodeError(raw []byte, cause error) error {
	kind, subtype := safeClaudeEventKind(raw)
	var mismatch *json.UnmarshalTypeError
	if errors.As(cause, &mismatch) {
		actual := strings.SplitN(mismatch.Value, " ", 2)[0]
		return fmt.Errorf("invalid Claude stream-json event: event_type=%s subtype=%s field=%s json_type=%s expected=%s", kind, subtype, strings.TrimPrefix(mismatch.Field, "eventFields."), actual, mismatch.Type.Kind())
	}
	var syntax *json.SyntaxError
	if errors.As(cause, &syntax) {
		return fmt.Errorf("invalid Claude stream-json event: JSON syntax at byte %d", syntax.Offset)
	}
	return errors.New("invalid Claude stream-json event: invalid JSON structure")
}

// safeClaudeEventKind 只发布已知协议枚举，未知事件标签也不能携带用户正文进入错误日志。
func safeClaudeEventKind(raw []byte) (string, string) {
	var header struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(raw, &header) != nil {
		return "unknown", "unknown"
	}
	kind := "unknown"
	switch header.Type {
	case "system", "user", "assistant", "result", "tool_progress", "tool_use_summary", "auth_status", "rate_limit_event", "stream_event", "control_response", "control_request", "keep_alive":
		kind = header.Type
	}
	subtype := "redacted"
	if safeClaudeSubtypeToken(header.Subtype) {
		subtype = header.Subtype
	}
	return kind, subtype
}

// decodeEventMessage 按event type区分system字符串元数据和用户/assistant的对象消息；其他类型仍严格拒绝。
func decodeEventMessage(event *Event, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if event.Type == "system" && bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		return json.Unmarshal(raw, &event.SystemMessage)
	}
	if err := json.Unmarshal(raw, &event.Message); err != nil {
		var mismatch *json.UnmarshalTypeError
		if errors.As(err, &mismatch) {
			field := mismatch.Field
			mismatch.Field = "message"
			if field != "" {
				mismatch.Field += "." + field
			}
		}
		return err
	}
	return nil
}

// safeClaudeSubtypeToken 允许有限长度小写协议标签用于诊断，禁止正文、路径或凭据进入日志。
func safeClaudeSubtypeToken(value string) bool {
	if len(value) > 48 {
		return false
	}
	for _, char := range value {
		if char != '_' && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}
