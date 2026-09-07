package standalone

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type recoverArchivedThreadKey struct{}

// Recover 恢复owner明确选定的同一Codex线程；仅此内部入口允许先解除provider归档。
func (e *Execution) Recover(ctx context.Context, activation orchestration.PersistedRuntimeActivation) (orchestration.LaunchResult, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return orchestration.LaunchResult{}, err
	}
	if generation, ok := contract.LaunchSeqFromContext(ctx); !ok || generation == 0 {
		return orchestration.LaunchResult{}, errors.New("CLI recovery requires owner launch generation")
	}
	if err := e.ActivatePersistedRuntime(context.WithValue(ctx, recoverArchivedThreadKey{}, true), activation); err != nil {
		return orchestration.LaunchResult{}, err
	}
	return orchestration.LaunchResult{ThreadID: activation.ThreadID, RemoteAgentID: activation.AgentID}, nil
}
