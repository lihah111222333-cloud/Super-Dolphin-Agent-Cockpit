package accesswait

import (
	"context"
	"fmt"
	"strings"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// authorizeWait 在读取 durable wait 前重新校验 caller、grant 和 identity fence。
func (s *Store) authorizeWait(ctx context.Context, request PollRequest, handle WaitHandle) error {
	if request.GrantRevision != handle.GrantRevision || request.CallerPrincipalRef != handle.CallerPrincipalRef || request.ScopeRef != handle.ScopeRef {
		return ErrAccessDenied
	}
	grant, err := s.GetGrant(ctx, handle.GrantID)
	if err != nil {
		return err
	}
	if grant.Revision != handle.GrantRevision {
		return ErrAccessDenied
	}
	if handle.SourceAgentID != grant.Source.AgentID || !equalFence(handle.Target, grant.Target) || handle.Selector.TargetAgentID != grant.Target.AgentID {
		return ErrAccessDenied
	}
	if !selectorSourceMatchesGrant(handle.Selector, grant) {
		return ErrAccessDenied
	}
	access := AccessRequest{
		RequestID: handle.WaitHandleID, GrantID: grant.GrantID,
		CallerPrincipalRef: request.CallerPrincipalRef, ScopeRef: request.ScopeRef,
		Capability: "read", Source: grant.Source, Target: grant.Target,
		RequestDigest: grant.RequestDigest, RequestedAt: s.now().UTC(),
	}
	return s.authorizeAgainstGrant(ctx, access, grant)
}

// authorizeWaitTx 在调用方事务中重读 grant 并校验 wait 的 caller、selector 与 identity fence。
func (s *Store) authorizeWaitTx(ctx context.Context, dbtx storedb.DBTX, request PollRequest, handle WaitHandle) (Authorization, error) {
	if !pollRequestMatchesHandle(request, handle) {
		return Authorization{}, ErrAccessDenied
	}
	grant, err := s.loadGrantTx(ctx, dbtx, handle.GrantID)
	if err != nil {
		return Authorization{}, err
	}
	access := accessRequestForWait(s, handle, grant)
	// The source fence is not persisted on a wait handle; AuthorizeTx loads it
	// from the grant, then the durable selector/target bindings are checked below.
	access.Source = grant.Source
	access.Target = grant.Target
	authorization, err := s.AuthorizeTx(ctx, dbtx, access)
	if err != nil {
		return Authorization{}, err
	}
	if !waitAuthorizationMatchesHandle(authorization, handle, grant) {
		return Authorization{}, ErrAccessDenied
	}
	return authorization, nil
}

// pollRequestMatchesHandle 保证事务授权使用的 caller、scope 和 grant revision 与句柄一致。
func pollRequestMatchesHandle(request PollRequest, handle WaitHandle) bool {
	return request.GrantRevision == handle.GrantRevision && request.CallerPrincipalRef == handle.CallerPrincipalRef && request.ScopeRef == handle.ScopeRef
}

// accessRequestForWait 构造绑定 durable grant source/target 的只读 capability 请求。
func accessRequestForWait(s *Store, handle WaitHandle, grant AccessGrant) AccessRequest {
	return AccessRequest{
		RequestID: handle.WaitHandleID, GrantID: handle.GrantID,
		CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef,
		Capability: "read", Source: grant.Source, Target: grant.Target,
		RequestDigest: handle.RequestDigest, RequestedAt: s.now().UTC(),
	}
}

// waitAuthorizationMatchesHandle 将授权结果与 durable wait 的代际和 selector fence 精确绑定。
func waitAuthorizationMatchesHandle(authorization Authorization, handle WaitHandle, grant AccessGrant) bool {
	return authorization.GrantRevision == handle.GrantRevision && handle.SourceAgentID == grant.Source.AgentID && equalFence(handle.Target, grant.Target) && handle.Selector.TargetAgentID == grant.Target.AgentID && selectorSourceMatchesGrant(handle.Selector, grant)
}

func (s *Store) loadGrantTx(ctx context.Context, dbtx storedb.DBTX, grantID string) (AccessGrant, error) {
	row, err := storedb.New(dbtx).GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: grantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("load access grant in transaction", err)
	}
	return grantFromRow(row)
}

// authorizeTerminalHandle 在 CAS 竞争后再次验证读取者，避免代际漂移泄漏终态事件。
func (s *Store) authorizeTerminalHandle(ctx context.Context, handle WaitHandle) error {
	return s.authorizeWait(ctx, PollRequest{
		WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef,
		ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision,
	}, handle)
}

// authorizeAgainstGrant 执行单次 capability 判决，不签发 provider permit。
func (s *Store) authorizeAgainstGrant(ctx context.Context, request AccessRequest, grant AccessGrant) error {
	if grant.Status == "revoked" {
		return ErrAccessRevoked
	}
	if grant.Status == "expired" || !s.now().UTC().Before(grant.ExpiresAt) {
		return ErrAccessExpired
	}
	if !grantIdentityMatches(request, grant) || !equalFence(request.Source, grant.Source) || !equalFence(request.Target, grant.Target) || !canonicalSourceFenceMatches(grant.Source) {
		return ErrAccessDenied
	}
	if !grantCapabilityAllows(request.Capability, grant) {
		return ErrAccessDenied
	}
	return s.verifyRequestedFences(ctx, grant.Source, grant.Target)
}

// selectorSourceMatchesGrant 将 durable selector 绑定到授权拥有的事件源。
//
// 同源旧形状读取 grant.Source 捕获的 caller canonical source。跨任务读取可
// 选择 grant.Target 的 canonical source，但必须是显式可读授权且 target fence
// 完整；caller/source fence 仍由 Authorize 独立校验，target fence 在消费 durable
// wait 前还会通过事务连接重读。
func selectorSourceMatchesGrant(selector EventSelector, grant AccessGrant) bool {
	if grant.Status != "active" || !validSelectorLane(selector.Lane) {
		return false
	}
	if canonicalSelectorMatchesFence(selector, grant.Source) {
		return true
	}
	return targetSelectorMatchesGrant(selector, grant)
}

// targetSelectorMatchesGrant 仅为 active、可读且完整的 target fence 放行跨任务 source。
func targetSelectorMatchesGrant(selector EventSelector, grant AccessGrant) bool {
	if grant.Status != "active" || !grant.CanRead || strings.ContainsAny(grant.Target.IncarnationID, "/\\") {
		return false
	}
	return canonicalSelectorMatchesFence(selector, grant.Target) && selector.TargetAgentID == grant.Target.AgentID
}

// canonicalSelectorMatchesFence 只比较 source tuple；完整 fence 必须由调用方
// 对当前 identity owner 独立重验。这里不比较 selector subject，因为同源兼容
// 允许 caller source lane 观察 target subject。
func canonicalSelectorMatchesFence(selector EventSelector, fence FenceSnapshot) bool {
	if err := fence.ValidateBound(); err != nil || !canonicalSourceFenceMatches(fence) {
		return false
	}
	return selector.SourceID == canonicalSourceID(fence) &&
		selector.SourceEpoch == canonicalSourceEpoch(fence)
}

func canonicalSourceFenceMatches(source FenceSnapshot) bool {
	return validateCanonicalSourceFence(source) == nil
}

func canonicalSourceID(source FenceSnapshot) string {
	return "task/" + source.AgentID
}

func canonicalSourceEpoch(source FenceSnapshot) string {
	return source.IncarnationID
}

func selectorSourceError(selector EventSelector, grant AccessGrant) error {
	if selectorSourceMatchesGrant(selector, grant) {
		return nil
	}
	return fmt.Errorf("%w: selector source does not match grant source or bound target", ErrAccessDenied)
}

func grantIdentityMatches(request AccessRequest, grant AccessGrant) bool {
	return grant.CallerPrincipalRef == request.CallerPrincipalRef && grant.ScopeRef == request.ScopeRef &&
		grant.Source.AgentID == request.Source.AgentID && grant.Target.AgentID == request.Target.AgentID
}

func grantCapabilityAllows(capability string, grant AccessGrant) bool {
	if capability == "read" {
		return grant.CanRead
	}
	if capability == "send" {
		return grant.CanSend
	}
	return false
}

// AuthorizeSend 是 send 的唯一授权前置；它只返回 grant/fence，不提交 provider effect。
func (s *Store) AuthorizeSend(ctx context.Context, request AccessRequest) (Authorization, error) {
	if request.Capability != "send" {
		return Authorization{}, ErrAccessDenied
	}
	return s.Authorize(ctx, request)
}
