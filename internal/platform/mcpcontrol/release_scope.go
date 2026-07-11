package mcpcontrol

import (
	"context"
	"errors"
	"slices"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// lspReleaseScopeDispatcher 是控制面暴露给上层的 LSP scope 释放能力。
type lspReleaseScopeDispatcher interface {
	DispatchLSPReleaseScope(ctx context.Context, req dto.LSPReleaseScopeRequest) (dto.LSPReleaseScopeResult, error)
}

// releaseScopeRequestFromConfigPayload 从 agent/thread 停止事件生成 LSP manager 释放请求。
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

// firstNonEmptyString 返回首个非空字符串，用于保留调用方原因并在缺失时回退到事件名。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// DispatchLSPReleaseScope 向可信 LSP peer 发送 manager 释放回调。
// 目标只允许精确 scope、同 agent LSP 或 shared LSP，绝不回退到无关 agent-bound peer。
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

// normalizeLSPReleaseScopeRequest 清理 scope 请求字段，避免空白字符影响路由匹配。
func normalizeLSPReleaseScopeRequest(req dto.LSPReleaseScopeRequest) dto.LSPReleaseScopeRequest {
	req.ScopeKind = strings.TrimSpace(req.ScopeKind)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.ManagerKey = strings.TrimSpace(req.ManagerKey)
	req.Reason = strings.TrimSpace(req.Reason)
	return req
}

// validateLSPReleaseScopeRequest 校验不同 scope kind 的必填字段，非法 scope 直接拒绝。
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

// releaseScopeTargets 根据 scope kind 选择 LSP peer，manager_key 请求广播给所有 active LSP。
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

// mergeLSPReleaseScopeResult 合并多个 LSP peer 的释放结果，计数累加且 key 列表去重。
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

// appendUniqueStrings 追加非空唯一字符串，保持原始出现顺序。
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
