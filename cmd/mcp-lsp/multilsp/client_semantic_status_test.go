package multilsp

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestClientWaitSemanticWorkspaceReady(t *testing.T) {
	c := &client{semanticStatusDone: make(chan struct{}), initialized: true, serverName: "rust-analyzer"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		_ = c.handleServerNotification(context.Background(), rustAnalyzerServerStatusMethod, json.RawMessage(`{"health":"ok","quiescent":false,"message":"loading"}`), nil)
		time.Sleep(10 * time.Millisecond)
		_ = c.handleServerNotification(context.Background(), rustAnalyzerServerStatusMethod, json.RawMessage(`{"health":"ok","quiescent":true,"message":"ready"}`), nil)
	}()
	if err := c.WaitSemanticWorkspaceReady(ctx); err != nil {
		t.Fatalf("WaitSemanticWorkspaceReady() error = %v", err)
	}
}

func TestClientWaitSemanticWorkspaceReadyFailsOnServerError(t *testing.T) {
	c := &client{semanticStatusDone: make(chan struct{}), initialized: true, serverName: "rust-analyzer"}
	if err := c.handleServerNotification(context.Background(), rustAnalyzerServerStatusMethod, json.RawMessage(`{"health":"error","quiescent":true,"message":"cargo metadata failed"}`), nil); err != nil {
		t.Fatalf("handleServerNotification() error = %v", err)
	}
	if err := c.WaitSemanticWorkspaceReady(context.Background()); err == nil {
		t.Fatal("WaitSemanticWorkspaceReady() error = nil, want server health error")
	}
}
