package taskcredential

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

const (
	currentBindingSchemaVersion = 1
	currentBindingStateActive   = "active"
)

// TaskSessionIdentityFactory 从已验证 principal、current binding 和 issuer 构造 transport 身份对。
type TaskSessionIdentityFactory struct{}

// Build 严格逐字段投影 active current binding；缺失或非法事实不会用默认值补齐。
func (TaskSessionIdentityFactory) Build(
	principal common.CallerPrincipal,
	current identity.CurrentBinding,
	issuer common.IssuerBinding,
) (common.TaskSessionIdentity, error) {
	if err := principal.Validate(); err != nil || !currentBindingIsActive(current) {
		return common.TaskSessionIdentity{}, ErrCredentialInputInvalid
	}
	attribution, err := common.NewCallerAttribution(
		current.AgentID,
		executionFenceFromCurrentBinding(current),
		issuer,
	)
	if err != nil {
		return common.TaskSessionIdentity{}, ErrCredentialInputInvalid
	}
	result := common.TaskSessionIdentity{Principal: principal, Attribution: attribution}
	if err := result.Validate(); err != nil {
		return common.TaskSessionIdentity{}, ErrCredentialInputInvalid
	}
	return result, nil
}

// CurrentTaskPrincipalResolver 是 verifier consumer 拥有的最小 current 身份读取端口。
type CurrentTaskPrincipalResolver interface {
	RequireCurrent(context.Context) (taskprincipal.TaskPrincipal, error)
}

var _ CurrentTaskPrincipalResolver = (*taskprincipal.Resolver)(nil)

// ResolverCurrentIdentityVerifier 把 taskprincipal current resolver 适配为 credential verifier。
type ResolverCurrentIdentityVerifier struct {
	resolver CurrentTaskPrincipalResolver
}

var _ CurrentIdentityVerifier = (*ResolverCurrentIdentityVerifier)(nil)

// NewResolverCurrentIdentityVerifier 构造 fail-closed adapter，并拒绝 nil 或 typed-nil resolver。
func NewResolverCurrentIdentityVerifier(
	resolver CurrentTaskPrincipalResolver,
) (*ResolverCurrentIdentityVerifier, error) {
	if interfaceIsNil(resolver) {
		return nil, ErrRegistryInvalid
	}
	return &ResolverCurrentIdentityVerifier{resolver: resolver}, nil
}

// Verify 在隔离的新 context 中绑定期望身份，并要求 resolver 返回同一 pair 和 current fence。
func (v *ResolverCurrentIdentityVerifier) Verify(
	ctx context.Context,
	expected common.TaskSessionIdentity,
) error {
	if v == nil || interfaceIsNil(v.resolver) || contextIsNil(ctx) || ctx.Err() != nil {
		return ErrCredentialUnavailable
	}
	bound, err := common.BindTaskSessionIdentity(valueIsolatedContext{Context: ctx}, expected)
	if err != nil {
		return ErrCredentialUnavailable
	}
	resolved, err := v.resolver.RequireCurrent(bound)
	if err != nil {
		return ErrCredentialUnavailable
	}
	resolvedIdentity := common.TaskSessionIdentity{
		Principal: resolved.Principal, Attribution: resolved.Attribution,
	}
	if !resolvedIdentity.Equal(expected) || !currentBindingMatchesIdentity(resolved.Binding, expected) {
		return ErrCredentialUnavailable
	}
	return nil
}

// valueIsolatedContext 保留取消和截止时间，但拒绝继承调用方可伪造的 context values。
type valueIsolatedContext struct {
	context.Context
}

// Value 固定隐藏父 context values，只有后续 BindTaskSessionIdentity 可写入身份。
func (valueIsolatedContext) Value(any) any { return nil }

// currentBindingIsActive 只接受 schema 1 的 active current binding。
func currentBindingIsActive(current identity.CurrentBinding) bool {
	return current.SchemaVersion == currentBindingSchemaVersion && current.State == currentBindingStateActive
}

// executionFenceFromCurrentBinding 对 current binding 的八个 transport fence 字段逐一投影。
func executionFenceFromCurrentBinding(current identity.CurrentBinding) common.ExecutionFence {
	return common.ExecutionFence{
		AgentID:               current.AgentID,
		ReservationID:         current.ReservationID,
		ReservationGeneration: current.ReservationGeneration,
		IncarnationID:         current.IncarnationID,
		IncarnationGeneration: current.IncarnationGeneration,
		BindingVersion:        current.BindingVersion,
		BindingDigest:         current.BindingDigest,
		SourceFenceRef:        current.SourceFenceRef,
	}
}

// currentBindingMatchesIdentity 要求 resolver binding 的 active schema 和完整 fence 精确匹配。
func currentBindingMatchesIdentity(
	current identity.CurrentBinding,
	expected common.TaskSessionIdentity,
) bool {
	return currentBindingIsActive(current) && executionFenceFromCurrentBinding(current) == expected.Attribution.Source
}
