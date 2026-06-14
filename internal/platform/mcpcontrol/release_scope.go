package mcpcontrol

import (
	"context"
	"errors"
	"slices"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

type lspReleaseScopeDispatcher interface {
	DispatchLSPReleaseScope(ctx context.Context, req dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error)
}

// releaseScopeRequestFromConfigPayload 从配置载荷处理release作用域请求。
func releaseScopeRequestFromConfigPayload(payload map[string]any) (dto.LSPReleaseScopeRequest, bool) {
	if payload == nil {
		return dto.LSPReleaseScopeRequest{}, false
	}
	eventName := configChangePayloadString(payload, "event")
	agentID := configChangePayloadString(payload, "agentId", "agent_id")
	threadID := configChangePayloadString(payload, "threadId", "thread_id")
	reason := configChangePayloadString(payload, "reason")
	switch eventName {
	case "agent/stopped":
		if agentID == "" {
			return dto.LSPReleaseScopeRequest{}, false
		}
		return dto.LSPReleaseScopeRequest{
			ScopeKind: dto.LSPReleaseScopeAgentAllThreads,
			AgentID:   agentID,
			Drain:     true,
			Reason:    firstNonEmptyString(reason, eventName),
		}, true
	case "thread/stopped":
		if agentID == "" || threadID == "" {
			return dto.LSPReleaseScopeRequest{}, false
		}
		return dto.LSPReleaseScopeRequest{
			ScopeKind: dto.LSPReleaseScopeAgentThread,
			AgentID:   agentID,
			ThreadID:  threadID,
			Drain:     true,
			Reason:    firstNonEmptyString(reason, eventName),
		}, true
	default:
		return dto.LSPReleaseScopeRequest{}, false
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// DispatchLSPReleaseScope sends the mcp-lsp admin callback to the exact
// trusted LSP peer for the scope, the same agent's LSP peer, or an explicit
// shared LSP peer. It never falls back to an unrelated agent-bound peer.
// DispatchLSPReleaseScope 派发LSPrelease作用域。
func (r *ToolRegistry) DispatchLSPReleaseScope(ctx context.Context, req dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error) {
	if r == nil {
		return dto.LSPReleaseScopeResult{}, errPeerUnavailable("mcp registry is nil")
	}
	req = normalizeLSPReleaseScopeRequest(req)
	if err := validateLSPReleaseScopeRequest(req); err != nil {
		return dto.LSPReleaseScopeResult{}, err
	}
	targets := r.releaseScopeTargets(req)
	if len(targets) == 0 {
		return dto.LSPReleaseScopeResult{}, nil
	}

	var combined dto.LSPReleaseScopeResult
	var errs []error
	for _, target := range targets {
		if target == nil || target.Peer == nil {
			continue
		}
		var result dto.LSPReleaseScopeResult
		callCtx, cancel := withTimeoutContext(ctx, r.notifyTimeout)
		err := target.Peer.Callback(callCtx, dto.MethodLSPReleaseScope, req, &result)
		cancel()
		if err != nil {
			peer, evicted := r.notePeerFailure(target.Lease)
			if evicted {
				_ = r.disconnectLease(target.Lease, disconnectLeaseOptions{
					ctx:  ctx,
					peer: peer,
				})
			} else {
				closePeer(peer)
			}
			errs = append(errs, errPeerUnavailable("mcp lsp release-scope callback failed for %s/%d: %v", target.Lease.InstanceID, target.Lease.Generation, err))
			continue
		}
		r.resetPeerFailure(target.Lease)
		mergeLSPReleaseScopeResult(&combined, result)
	}
	return combined, errors.Join(errs...)
}

func normalizeLSPReleaseScopeRequest(req dto.LSPReleaseScopeRequest) dto.LSPReleaseScopeRequest {
	req.ScopeKind = strings.TrimSpace(req.ScopeKind)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.ManagerKey = strings.TrimSpace(req.ManagerKey)
	req.Reason = strings.TrimSpace(req.Reason)
	return req
}

// validateLSPReleaseScopeRequest 校验LSPrelease作用域请求。
func validateLSPReleaseScopeRequest(req dto.LSPReleaseScopeRequest) error {
	switch req.ScopeKind {
	case dto.LSPReleaseScopeAgentThread:
		if req.AgentID == "" || req.ThreadID == "" {
			return errInvalidParams("lsp release scope agent_thread requires agent_id and thread_id")
		}
	case dto.LSPReleaseScopeAgentAllThreads:
		if req.AgentID == "" {
			return errInvalidParams("lsp release scope agent_all_threads requires agent_id")
		}
	case dto.LSPReleaseScopeManagerKey:
		if req.ManagerKey == "" {
			return errInvalidParams("lsp release scope manager_key requires manager_key")
		}
	default:
		return errInvalidParams("unsupported lsp release scope kind %q", req.ScopeKind)
	}
	return nil
}

func (r *ToolRegistry) releaseScopeTargets(req dto.LSPReleaseScopeRequest) []*ToolInstance {
	if req.ScopeKind == dto.LSPReleaseScopeManagerKey {
		return r.FindActiveByKind(dto.ClientKindLSP)
	}
	return r.FindActiveForScope(ToolScope{
		Family:   dto.ClientKindLSP,
		AgentID:  req.AgentID,
		ThreadID: req.ThreadID,
	})
}

func mergeLSPReleaseScopeResult(dst *dto.LSPReleaseScopeResult, src dto.LSPReleaseScopeResult) {
	if dst == nil {
		return
	}
	dst.MatchedManagers += src.MatchedManagers
	dst.ClosedManagers += src.ClosedManagers
	dst.BusyLeases += src.BusyLeases
	dst.Drained = dst.Drained || src.Drained
	dst.ScopeKeys = appendUniqueStrings(dst.ScopeKeys, src.ScopeKeys...)
	dst.ManagerKeys = appendUniqueStrings(dst.ManagerKeys, src.ManagerKeys...)
}

func appendUniqueStrings(dst []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || slices.Contains(dst, value) {
			continue
		}
		dst = append(dst, value)
	}
	return dst
}
