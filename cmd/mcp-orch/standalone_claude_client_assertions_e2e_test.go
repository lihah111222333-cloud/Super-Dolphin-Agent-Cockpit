//go:build e2e

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestStandaloneClaudeStreamAssertions 覆盖真实回执关联和失败关闭边界。
func TestStandaloneClaudeStreamAssertions(t *testing.T) {
	t.Run("host_default_environment", testClaudeHostDefaultEnvironment)
	use := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"mcp__orch__list_agents"}]}}`
	receipt := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"[]"}]}}`
	result := `{"type":"result","subtype":"success","is_error":false,"result":"ORCH_TOOL_CALL_OK"}`
	good := use + "\n" + receipt + "\n" + result
	cases := []struct {
		name, stream string
		valid        bool
	}{
		{"success", good, true},
		{"missing_receipt", use + "\n" + result, false},
		{"wrong_id", strings.Replace(good, `"tool_use_id":"call1"`, `"tool_use_id":"other"`, 1), false},
		{"tool_error", strings.Replace(good, `"content":"[]"`, `"content":"failed","is_error":true`, 1), false},
		{"multiple_calls", use + "\n" + good, false},
		{"model_failure", strings.Replace(good, `"subtype":"success"`, `"subtype":"error_max_turns"`, 1), false},
		{"model_is_error", strings.Replace(good, `"is_error":false`, `"is_error":true`, 1), false},
		{"malformed", good + "\n{", false},
		{"wrong_marker", strings.Replace(good, "ORCH_TOOL_CALL_OK", "prefix ORCH_TOOL_CALL_OK", 1), false},
		{"missing_result", use + "\n" + receipt, false},
		{"missing_success_flag", strings.Replace(good, `"is_error":false,`, "", 1), false},
		{"wrong_tool", strings.Replace(good, "mcp__orch__list_agents", "mcp__orch__other", 1), false},
		{"missing_call_id", strings.Replace(good, `"id":"call1",`, "", 1), false},
		{"receipt_before_call", receipt + "\n" + use + "\n" + result, false},
		{"duplicate_receipt", use + "\n" + receipt + "\n" + receipt + "\n" + result, false},
		{"nested_result", strings.Replace(good, `"subtype":"success"`, `"parent_tool_use_id":"nested","subtype":"success"`, 1), false},
		{"trailing_event", good + "\n" + use, false},
		{"empty_receipt", strings.Replace(good, `"content":"[]"`, `"content":null`, 1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateClaudeToolCallStream(tc.stream)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

// claudeToolFrame 只消费 CLI stream-json 的调用、回执及最终结果字段。
type claudeToolFrame struct {
	Type            string  `json:"type"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Subtype         string  `json:"subtype"`
	IsError         *bool   `json:"is_error"`
	Result          string  `json:"result"`
	Message         struct {
		Content []claudeToolContent `json:"content"`
	} `json:"message"`
}

type claudeToolContent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

type claudeToolEvidence struct {
	callID              string
	received, completed bool
}

// validateClaudeToolCallStream 拒绝畸形帧，只有完整调用及匹配成功回执才接受最终 marker。
func validateClaudeToolCallStream(output string) error {
	var evidence claudeToolEvidence
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		var frame claudeToolFrame
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			return fmt.Errorf("invalid Claude JSON frame: %w", err)
		}
		if frame.Type == "" {
			return fmt.Errorf("missing Claude frame type")
		}
		if evidence.completed {
			return fmt.Errorf("frame after final result")
		}
		if err := evidence.accept(frame); err != nil {
			return err
		}
	}
	if !evidence.completed {
		return fmt.Errorf("missing successful final result")
	}
	return nil
}

// accept 将根会话事件逐条关联到唯一调用，不能用模型文本替代工具回执。
func (e *claudeToolEvidence) accept(frame claudeToolFrame) error {
	if frame.ParentToolUseID != nil {
		return fmt.Errorf("unexpected nested session")
	}
	if frame.Type == "result" {
		return e.finish(frame)
	}
	for _, block := range frame.Message.Content {
		switch block.Type {
		case "tool_use":
			if err := e.use(frame.Type, block); err != nil {
				return err
			}
		case "tool_result":
			if err := e.receipt(frame.Type, block); err != nil {
				return err
			}
		}
	}
	return nil
}

// receipt 校验工具回执身份、错误标志和非空内容。
func (e *claudeToolEvidence) receipt(frameType string, block claudeToolContent) error {
	if frameType != "user" || e.callID == "" || block.ToolUseID != e.callID || e.received || block.IsError {
		return fmt.Errorf("missing, mismatched, repeated or failed tool receipt")
	}
	content := strings.TrimSpace(string(block.Content))
	if content == "" || content == "null" || content == `""` || content == "[]" {
		return fmt.Errorf("empty tool receipt")
	}
	e.received = true
	return nil
}

// finish 要求根模型结果明确成功且 marker 精确匹配。
func (e *claudeToolEvidence) finish(frame claudeToolFrame) error {
	if !e.received || frame.Subtype != "success" || frame.IsError == nil || *frame.IsError || frame.Result != "ORCH_TOOL_CALL_OK" {
		return fmt.Errorf("unsuccessful final Claude result")
	}
	e.completed = true
	return nil
}

// use 拒绝重复调用或其他工具并记录唯一调用 ID。
func (e *claudeToolEvidence) use(frameType string, block claudeToolContent) error {
	if frameType != "assistant" || block.Name != "mcp__orch__list_agents" || block.ID == "" || e.callID != "" {
		return fmt.Errorf("unexpected or repeated tool use: name=%q already_called=%t", block.Name, e.callID != "")
	}
	e.callID = block.ID
	return nil
}

// testClaudeHostDefaultEnvironment 验证认证只能来自宿主默认配置或 keychain 而非注入凭据。
func testClaudeHostDefaultEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "synthetic-test-key")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "synthetic-test-token")
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	env := claudeToolCallEnv(t)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "ANTHROPIC_") || key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_CODE_OAUTH_TOKEN" {
			t.Fatalf("host_default inherited forbidden environment key %s", key)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "HOME="+home+"\n") {
		t.Fatal("host_default lost host HOME")
	}
}
