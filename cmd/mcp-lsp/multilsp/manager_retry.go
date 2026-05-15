package multilsp

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

func canAutoRetryDeadClientRequest(method string) bool {
	switch method {
	case protocol.MethodHover,
		protocol.MethodDefinition,
		protocol.MethodImplementation,
		protocol.MethodTypeDefinition,
		protocol.MethodReferences,
		protocol.MethodDocumentSymbol,
		protocol.MethodCompletion,
		protocol.MethodSignatureHelp,
		protocol.MethodFoldingRange,
		protocol.MethodSemanticTokensFull,
		protocol.MethodPrepareCallHierarchy,
		protocol.MethodCallHierarchyIncoming,
		protocol.MethodCallHierarchyOutgoing,
		protocol.MethodPrepareTypeHierarchy,
		protocol.MethodTypeHierarchySupertypes,
		protocol.MethodTypeHierarchySubtypes,
		protocol.MethodWorkspaceSymbol:
		return true
	default:
		return false
	}
}

func (m *manager) nonReplayableDeadClientError(ctx context.Context, client Client, err error) error {
	if repairErr := m.rebuildClientAfterNonReplayableFailure(ctx, client); repairErr != nil {
		return errors.Join(ErrClientClosed, err, repairErr)
	}
	return errors.Join(ErrClientClosed, err)
}

func (m *manager) rebuildClientAfterNonReplayableFailure(ctx context.Context, client Client) error {
	replacement, err := m.rebuildClientAfterFailure(ctx, client, true)
	if err != nil {
		return err
	}
	if replacement == nil {
		return ErrClientClosed
	}
	return nil
}
