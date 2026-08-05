package main

import (
	"bufio"
	"encoding/json"
	"os"
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	if run() != nil {
		os.Exit(1)
	}
}

func run() error {
	in := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	for in.Scan() {
		var m message
		if err := json.Unmarshal(in.Bytes(), &m); err != nil {
			return err
		}
		if err := handleMessage(out, m); err != nil {
			return err
		}
	}
	if err := in.Err(); err != nil {
		return err
	}
	return out.Flush()
}

// handleMessage 为离线生命周期测试提供固定的最小 ACP 对端行为。
func handleMessage(out *bufio.Writer, m message) error {
	switch m.Method {
	case "initialize":
		return writeInitialize(out, m.ID)
	case "session/new":
		return write(out, message{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{"sessionId": "fake-session"}})
	case "session/load", "session/resume", "session/close":
		return write(out, message{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{}})
	case "session/prompt":
		return handlePrompt(out, m)
	case "session/cancel", "$/cancel_request":
		return nil
	default:
		return writeUnknown(out, m)
	}
}

func writeInitialize(out *bufio.Writer, id json.RawMessage) error {
	return write(out, message{JSONRPC: "2.0", ID: id, Result: map[string]any{
		"protocolVersion": 1,
		"agentCapabilities": map[string]any{
			"loadSession":         true,
			"sessionCapabilities": map[string]any{"resume": true},
		},
	}})
}

func handlePrompt(out *bufio.Writer, m message) error {
	var params struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(m.Params, &params); err != nil || params.SessionID == "" {
		return write(out, message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: -32602, Message: "invalid params"}})
	}
	update, err := raw(map[string]any{"sessionId": params.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk"}})
	if err != nil {
		return err
	}
	if err := write(out, message{JSONRPC: "2.0", Method: "session/update", Params: update}); err != nil {
		return err
	}
	return write(out, message{JSONRPC: "2.0", ID: m.ID, Result: map[string]any{"stopReason": "end_turn"}})
}

func writeUnknown(out *bufio.Writer, m message) error {
	if len(m.ID) == 0 {
		return nil
	}
	return write(out, message{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})
}

func write(out *bufio.Writer, m message) error {
	if err := json.NewEncoder(out).Encode(m); err != nil {
		return err
	}
	return out.Flush()
}

func raw(value any) (json.RawMessage, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
