package wails

import (
	"context"
	"encoding/json"
	"reflect"
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
	if method != "agent/list" {
		t.Fatalf("ListAgents() method = %q, want %q", method, "agent/list")
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

func TestGetGroupUsesLegacyFallback(t *testing.T) {
	app := &App{group: "team-alpha"}
	if got := app.GetGroup(); got != "team-alpha" {
		t.Fatalf("GetGroup() = %q, want %q", got, "team-alpha")
	}
}

func TestGetGroupReturnsNonEmptyDefault(t *testing.T) {
	if got := (&App{}).GetGroup(); got != defaultGroup {
		t.Fatalf("GetGroup() = %q, want %q", got, defaultGroup)
	}
	if defaultGroup == "" {
		t.Fatal("defaultGroup = empty, want non-empty default")
	}
}

func TestWindowBootstrapSnapshotCodecRoundTrip(t *testing.T) {
	origin := map[string]any{
		"page": "chat",
		"thread": map[string]any{
			"activeThreadId": "thread-123",
		},
	}
	encoded, err := encodeWindowBootstrapSnapshot(origin)
	if err != nil {
		t.Fatalf("encodeWindowBootstrapSnapshot() error = %v", err)
	}
	if encoded == "" {
		t.Fatal("encodeWindowBootstrapSnapshot() = empty, want payload")
	}
	decoded, err := decodeWindowBootstrapSnapshot(encoded)
	if err != nil {
		t.Fatalf("decodeWindowBootstrapSnapshot() error = %v", err)
	}
	if !reflect.DeepEqual(decoded, origin) {
		t.Fatalf("decoded snapshot = %#v, want %#v", decoded, origin)
	}
}

func TestConsumeWindowBootstrapSnapshotFallsBackToLegacySlot(t *testing.T) {
	app := &App{windowBootstrap: map[string]any{"page": "chat"}}

	first := app.consumeWindowBootstrapSnapshot()
	if first["page"] != "chat" {
		t.Fatalf("first snapshot = %#v, want page=chat", first)
	}
	if second := app.consumeWindowBootstrapSnapshot(); second != nil {
		t.Fatalf("second snapshot = %#v, want nil", second)
	}
}

func TestEmitRuntimeEventDoesNotMirrorCompatEnvelopesToRPC(t *testing.T) {
	var emitted []string
	var pushed []string
	app := &App{
		emitter: func(event string, _ any) {
			emitted = append(emitted, event)
		},
		pushRuntimeEvent: func(_ context.Context, event string, _ any) {
			pushed = append(pushed, event)
		},
	}

	app.emitRuntimeEvent(bridgeEventName, map[string]any{"type": "item/agentMessage/delta"})
	app.emitRuntimeEvent(agentEventName, map[string]any{"type": "item/agentMessage/delta"})
	app.emitRuntimeEvent("app-will-quit", nil)

	if want := []string{bridgeEventName, agentEventName, "app-will-quit"}; !reflect.DeepEqual(emitted, want) {
		t.Fatalf("native emitted events = %v, want %v", emitted, want)
	}
	if want := []string{"app-will-quit"}; !reflect.DeepEqual(pushed, want) {
		t.Fatalf("RPC pushed events = %v, want %v", pushed, want)
	}
}
