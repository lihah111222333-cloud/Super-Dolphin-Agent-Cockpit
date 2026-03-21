package wails

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLaunchAgentUsesThreadStart(t *testing.T) {
	var method string
	var params map[string]string
	app := &App{dispatch: func(ctx context.Context, gotMethod string, raw json.RawMessage) (json.RawMessage, error) {
		method = gotMethod
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatalf("unmarshal launch params: %v", err)
		}
		return json.RawMessage(`{"threadId":"thread-1"}`), nil
	}}

	result, err := app.LaunchAgent("Agent 1", "hello", " /tmp/project ")
	if err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	if method != "thread/start" {
		t.Fatalf("LaunchAgent() method = %q, want %q", method, "thread/start")
	}
	if params["cwd"] != "/tmp/project" {
		t.Fatalf("LaunchAgent() cwd = %q, want %q", params["cwd"], "/tmp/project")
	}
	if params["baseInstructions"] != "hello" {
		t.Fatalf("LaunchAgent() baseInstructions = %q, want %q", params["baseInstructions"], "hello")
	}
	if _, exists := params["name"]; exists {
		t.Fatal("LaunchAgent() forwarded legacy name to thread/start, want it omitted")
	}
	got, ok := result.(map[string]any)
	if !ok || got["threadId"] != "thread-1" {
		t.Fatalf("LaunchAgent() result = %#v, want decoded threadId", result)
	}
}

func TestStopAgentUsesThreadStop(t *testing.T) {
	var method string
	var params map[string]string
	app := &App{dispatch: func(ctx context.Context, gotMethod string, raw json.RawMessage) (json.RawMessage, error) {
		method = gotMethod
		if err := json.Unmarshal(raw, &params); err != nil {
			t.Fatalf("unmarshal stop params: %v", err)
		}
		return json.RawMessage(`{"ok":true}`), nil
	}}

	if err := app.StopAgent(" thread-7 "); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}
	if method != "thread/stop" {
		t.Fatalf("StopAgent() method = %q, want %q", method, "thread/stop")
	}
	if params["threadId"] != "thread-7" {
		t.Fatalf("StopAgent() threadId = %q, want %q", params["threadId"], "thread-7")
	}
}

func TestListAgentsUsesAgentList(t *testing.T) {
	var method string
	var payload string
	app := &App{dispatch: func(ctx context.Context, gotMethod string, raw json.RawMessage) (json.RawMessage, error) {
		method = gotMethod
		payload = string(raw)
		return json.RawMessage(`[{"id":"agent-1"}]`), nil
	}}

	result, err := app.ListAgents()
	if err != nil {
		t.Fatalf("ListAgents() error = %v", err)
	}
	if method != "agent.list" {
		t.Fatalf("ListAgents() method = %q, want %q", method, "agent.list")
	}
	if payload != "{}" {
		t.Fatalf("ListAgents() payload = %q, want %q", payload, "{}")
	}
	items, ok := result.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("ListAgents() result = %#v, want decoded list", result)
	}
}

func TestOpenNewWindowRequiresApplication(t *testing.T) {
	app := &App{}
	err := app.OpenNewWindow("group", 1, "bootstrap", "/tmp")
	if err == nil {
		t.Fatal("OpenNewWindow() error = nil, want application readiness error")
	}
	if !strings.Contains(err.Error(), "application is not ready") {
		t.Fatalf("OpenNewWindow() error = %q, want application readiness marker", err)
	}
}

func TestDeferredBindingsReturnNotImplemented(t *testing.T) {
	app := &App{}
	cases := []struct {
		name string
		call func() error
	}{
		{
			name: "GetLSPDiagnostics",
			call: func() error {
				_, err := app.GetLSPDiagnostics("/tmp/file.go")
				return err
			},
		},
		{
			name: "GetLSPStatus",
			call: func() error {
				_, err := app.GetLSPStatus()
				return err
			},
		},
	}
	for _, tc := range cases {
		err := tc.call()
		if err == nil {
			t.Fatalf("%s() error = nil, want not implemented", tc.name)
		}
		if !strings.Contains(err.Error(), "not implemented") {
			t.Fatalf("%s() error = %q, want not implemented marker", tc.name, err)
		}
	}
}
