package eventsurface

import (
	"context"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	"github.com/kelindar/event"
)

type publishedEvent struct {
	method  string
	payload any
}

func TestBindPublishesExpandedSurface(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	got := make(chan publishedEvent, 3)
	cancels := Bind(dispatcher, nil, func(method string, payload any) {
		got <- publishedEvent{method: method, payload: payload}
	})
	defer cancelAll(cancels)

	now := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, threaddto.Started{
		EventHeader:      shared.EventHeader{Timestamp: now},
		ThreadID:         "thread-1",
		AgentID:          "agent-1",
		ProviderThreadID: "provider-thread-1",
		CWD:              "/tmp/demo",
	})
	event.Publish(dispatcher, workspacedto.WorkspaceRunCreated{
		WorkspaceRunHeader: shared.WorkspaceRunHeader{
			DAGHeader: shared.DAGHeader{
				EventHeader: shared.EventHeader{Timestamp: now},
				DagKey:      "dag-1",
			},
			RunKey: "run-1",
		},
		SourceRoot:    "/src",
		WorkspacePath: "/workspace/run-1",
		Status:        "active",
		CreatedBy:     "alice",
		UpdatedBy:     "alice",
	})
	event.Publish(dispatcher, agentdto.AgentStopped{
		AgentSessionHeader: shared.AgentSessionHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: now},
					ThreadID:    "thread-1",
				},
				AgentID: "agent-1",
			},
			SessionID: "session-1",
		},
		Reason: "done",
	})

	seen := map[string]bool{}
	for range 3 {
		seen[mustReceivePublished(t, got).method] = true
	}
	for _, method := range []string{MethodThreadStarted, MethodWorkspaceCreated, MethodAgentStopped} {
		if !seen[method] {
			t.Fatalf("method %q missing from %#v", method, seen)
		}
	}
}

func TestWorkspacePayloadShapes(t *testing.T) {
	t.Parallel()

	now := time.Unix(1710000000, 0).UTC()
	created := workspaceCreatedPayload(workspacedto.WorkspaceRunCreated{
		WorkspaceRunHeader: shared.WorkspaceRunHeader{
			DAGHeader: shared.DAGHeader{
				EventHeader: shared.EventHeader{Timestamp: now},
				DagKey:      "dag-2",
			},
			RunKey: "run-2",
		},
		SourceRoot:    "/src",
		WorkspacePath: "/workspace/run-2",
		Status:        "active",
		CreatedBy:     "bob",
		UpdatedBy:     "bob",
	})
	run, _ := created["run"].(map[string]any)
	if created["runKey"] != "run-2" || run["status"] != "active" {
		t.Fatalf("created payload = %#v", created)
	}

	merged := workspaceMergedPayload(workspacedto.WorkspaceRunMerged{
		WorkspaceRunHeader: shared.WorkspaceRunHeader{RunKey: "run-2"},
		Status:             "merged",
		SourceRoot:         "/src",
		WorkspacePath:      "/workspace/run-2",
		MergedFileCount:    3,
		Removed:            1,
	})
	result, _ := merged["result"].(map[string]any)
	if result["merged"] != 3 || result["removed"] != 1 {
		t.Fatalf("merged payload = %#v", merged)
	}
}

func cancelAll(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func mustReceivePublished(t *testing.T, ch <-chan publishedEvent) publishedEvent {
	t.Helper()

	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("expected published event")
		return publishedEvent{}
	}
}
