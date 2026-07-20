package wails

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type failureMatrixTriggerParams struct {
	CaseID string `json:"caseId"`
}

func TestFailureMatrixWailsTerminalBoundary(t *testing.T) {
	t.Parallel()

	t.Run("FM-01", func(t *testing.T) {
		dispatcher, emitted := newFailureMatrixWailsBridge(t)
		server := platformrpc.NewServer(platformrpc.Params{
			Config: &contract.Config{RPCAddr: "127.0.0.1:0"},
		})
		server.Register(handler.Map{
			"failure-matrix/terminal-failed": platformrpc.StrictHandler(
				func(_ context.Context, params failureMatrixTriggerParams) (map[string]any, error) {
					if params.CaseID != "FM-01" {
						return nil, fmt.Errorf("unexpected failure matrix case %q", params.CaseID)
					}
					terminal := failureMatrixTerminal()
					projected, err := eventsurface.ProjectRemoteTurnTerminal(terminal, "agent-matrix")
					if err != nil {
						return nil, err
					}
					event.Publish(dispatcher, projected)
					return map[string]any{"ok": true, "caseId": params.CaseID}, nil
				},
			),
		})
		app := &App{dispatch: server.Dispatch}
		result, err := app.CallAPI(
			"failure-matrix/terminal-failed",
			json.RawMessage(`{"caseId":"FM-01"}`),
		)
		if err != nil {
			t.Fatalf("App.CallAPI() error = %v", err)
		}
		response, ok := result.(map[string]any)
		if !ok || response["ok"] != true || response["caseId"] != "FM-01" {
			t.Fatalf("App.CallAPI() result = %#v, want strict RPC response", result)
		}

		select {
		case envelope := <-emitted:
			assertFailureMatrixWailsEnvelope(t, envelope, failureMatrixTerminal())
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for Wails terminal event")
		}
	})
}

func newFailureMatrixWailsBridge(t *testing.T) (*event.Dispatcher, <-chan map[string]any) {
	t.Helper()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() {
		if err := dispatcher.Close(); err != nil {
			t.Errorf("close dispatcher: %v", err)
		}
	})
	emitted := make(chan map[string]any, 1)
	lifecycle := NewWailsLifecycle(nil, nil)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		envelope, ok := payload.(map[string]any)
		if name == bridgeEventName && ok && envelope["type"] == eventsurface.MethodTurnTerminal {
			emitted <- envelope
		}
	})
	bridge := NewEventBridge(dispatcher, lifecycle, nil)
	bridge.Start()
	t.Cleanup(bridge.Stop)
	return dispatcher, emitted
}

func failureMatrixTerminal() turndto.TurnTerminalV2 {
	return turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "failure-matrix-event-1",
		ThreadID:      "thread-matrix",
		TurnID:        "turn-1",
		Outcome:       "failed",
		PublicError: &turndto.PublicErrorV1{
			Code:            "PROVIDER_FAILED",
			Title:           "运行失败",
			Message:         "提供方未能完成本轮响应",
			DiagnosticID:    "diag-matrix",
			Retryable:       false,
			RecoveryActions: []string{"copy_diagnostics"},
		},
		PartialItemIDs: []string{"partial-1"},
		OccurredAt:     "2026-07-17T01:02:03Z",
	}
}

func assertFailureMatrixWailsEnvelope(t *testing.T, envelope map[string]any, terminal turndto.TurnTerminalV2) {
	t.Helper()
	payload, ok := envelope["payload"].(map[string]any)
	if !ok {
		t.Fatalf("Wails bridge payload type = %T, want map", envelope["payload"])
	}
	if payload["outcome"] != "failed" || payload["eventId"] != terminal.EventID {
		t.Fatalf("Wails bridge terminal = %#v, want canonical failed terminal", payload)
	}
	partial, ok := payload["partialItemIds"].([]any)
	if !ok || len(partial) != 1 || partial[0] != "partial-1" {
		t.Fatalf("Wails bridge partialItemIds = %#v, want [partial-1]", payload["partialItemIds"])
	}
	if _, leaked := payload["success"]; leaked {
		t.Fatalf("Wails bridge leaked legacy success field: %#v", payload)
	}
}
