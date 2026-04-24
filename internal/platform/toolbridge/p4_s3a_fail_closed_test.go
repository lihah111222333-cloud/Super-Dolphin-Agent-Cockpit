package toolbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// TestToolbridgePersistentSubagentRejectsMissingRuntime is the P22 P4 S3a
// umbrella guard (test name pre-registered at P4 §TDD line 257). It
// locks the invariant that the `spawn_agent` policy gate fails closed —
// returning contract.ErrThreadRuntimeRequired or
// contract.ErrPersistentSubagentRuntimeRequired — instead of silently
// falling back to `cfg.Agent.PersistentSubagentDefault` when thread or
// runtime identity is missing.
//
// Pre-P4 drift (P4 plan §51 / §141): persistentSubagentRequired used to
// read cfg.Agent.PersistentSubagentDefault any time runtime lookup
// failed, making "missing thread row" silently behave like the global
// default. The e84b688 fix landed fail-closed behavior; this test is
// the shared behavioral anchor that pre-existing
// TestToolBridge_RejectsSpawnAgent{Without,Stored}Runtime tests roll
// into the single name promised by P4 §257.
//
// Contract (P4 §98-99 / §276):
//   - Missing thread identity  → contract.ErrThreadRuntimeRequired
//   - Missing runtime config   → contract.ErrPersistentSubagentRuntimeRequired
//   - PersistentSubagentDefault is only consulted when thread AND runtime
//     were successfully loaded; it may not be borrowed to "recover" from
//     a failed identity lookup. To prove this we enable
//     PersistentSubagentDefault on the handler config and still expect
//     the missing-identity cases to error out — if the old fallback were
//     still in place, the handler would succeed and emit the block
//     message instead.
func TestToolbridgePersistentSubagentRejectsMissingRuntime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		handlerSetup func(h *Handler)
		req          ToolCallRequest
		wantErr      error
	}{
		{
			name: "missing thread identity",
			// Default newHandlerForTest has no bindingStore / threadStore
			// wired, so thread identity cannot be resolved.
			handlerSetup: nil,
			req: ToolCallRequest{
				Name:      "spawn_agent",
				Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
			},
			wantErr: contract.ErrThreadRuntimeRequired,
		},
		{
			name: "missing runtime row",
			handlerSetup: func(h *Handler) {
				h.threadStore = &stubThreadStore{}
			},
			req: ToolCallRequest{
				Name:      "spawn_agent",
				Arguments: mustRawJSON(t, map[string]any{"message": "create child agent"}),
				ThreadID:  "thread-missing-runtime",
			},
			wantErr: contract.ErrPersistentSubagentRuntimeRequired,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandlerForTest()
			// Enable PersistentSubagentDefault so this test would PASS
			// under the pre-P4 silent-fallback behavior but must still
			// FAIL-closed under the post-e84b688 contract.
			h.cfg = &platformconfig.Config{
				Agent: platformconfig.AgentConfig{PersistentSubagentDefault: true},
			}
			if tt.handlerSetup != nil {
				tt.handlerSetup(h)
			}

			got, err := h.routeToolCall(context.Background(), tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("routeToolCall() error = %v, want %v (fail-closed even with PersistentSubagentDefault=true)", err, tt.wantErr)
			}
			if got != nil {
				t.Fatalf("routeToolCall() result = %#v, want nil (no block message should leak when identity is missing)", got)
			}
		})
	}
}
