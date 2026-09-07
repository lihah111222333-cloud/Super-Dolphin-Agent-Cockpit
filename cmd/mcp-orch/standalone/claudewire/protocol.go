// Package claudewire 定义官方 Claude CLI stream-json 协议边界。
package claudewire

import (
	"encoding/json"
	"errors"
)

// Event 保留独立执行器消费的 CLI 事件字段；正文不能被当成控制命令。
type Event struct {
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
	if err := json.Unmarshal(raw, &event); err != nil {
		return Event{}, errors.New("invalid Claude stream-json event")
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
