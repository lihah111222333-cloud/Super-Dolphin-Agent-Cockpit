package uistate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

func TestUIStateGetAcceptsKnownDiffRevision(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	server := rpc.NewServer(rpc.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)
	_, err = server.Dispatch(context.Background(), "ui/state/get", json.RawMessage(`{"threadId":"thread-1","includeDiff":true,"knownDiffRevision":7}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/state/get) error = %v", err)
	}
}
