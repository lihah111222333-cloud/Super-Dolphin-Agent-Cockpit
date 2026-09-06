package commandintent

import (
	"context"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// validateAuthorization 比较原请求与owner返回的完整grant判决。
func validateAuthorization(request accesswait.AccessRequest, authorization accesswait.Authorization) error {
	if err := authorization.Validate(); err != nil {
		return err
	}
	if authorization.RequestID != request.RequestID || authorization.GrantID != request.GrantID || authorization.Capability != "send" {
		return accesswait.ErrAccessDenied
	}
	grant := authorization.Grant
	if grant.CallerPrincipalRef != request.CallerPrincipalRef || grant.ScopeRef != request.ScopeRef {
		return accesswait.ErrAccessDenied
	}
	if !equalFence(request.Source, grant.Source) || !equalFence(request.Target, grant.Target) {
		return accesswait.ErrAccessDenied
	}
	return nil
}

func (a *Assembler) buildSignedDraft(ctx context.Context, request ReserveAuthorizedSendDraftRequest, principal common.CallerPrincipal, authorization accesswait.Authorization, source, target identity.CurrentBinding, contentRef string) (orchestrationcommand.Intent, orchestrationcommand.SendMessageProfile, error) {
	intent := orchestrationcommand.Intent{
		CommandID: request.CandidateCommandID, CommandKind: "send_message",
		CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef,
		IdempotencyNamespace: request.IdempotencyNamespace, IdempotencyKeyDigest: request.IdempotencyKeyDigest,
		RequestDigest: zeroDigest(), DigestKeyID: request.DigestKeyID,
		RequestContentRef: contentRef,
		SourceFenceRef:    authorization.Grant.Source.SourceFenceRef, TargetFenceRef: authorization.Grant.Target.SourceFenceRef,
	}
	profile, err := profileFromOwners(request, principal, authorization, source, target)
	if err != nil {
		return orchestrationcommand.Intent{}, orchestrationcommand.SendMessageProfile{}, err
	}
	digest, err := a.signer.SignSendProfileDigest(ctx, intent, profile)
	if err != nil {
		return orchestrationcommand.Intent{}, orchestrationcommand.SendMessageProfile{}, err
	}
	intent.RequestDigest = digest
	profile.CompleteIntentDigest = digest
	if err := profile.Validate(intent); err != nil {
		return orchestrationcommand.Intent{}, orchestrationcommand.SendMessageProfile{}, err
	}
	return intent, profile, nil
}

func profileFromOwners(request ReserveAuthorizedSendDraftRequest, principal common.CallerPrincipal, authorization accesswait.Authorization, source, target identity.CurrentBinding) (orchestrationcommand.SendMessageProfile, error) {
	sourceSelector, err := selectorFromBinding(source)
	if err != nil {
		return orchestrationcommand.SendMessageProfile{}, err
	}
	targetSelector, err := selectorFromBinding(target)
	if err != nil {
		return orchestrationcommand.SendMessageProfile{}, err
	}
	return orchestrationcommand.SendMessageProfile{
		Kind: orchestrationcommand.SendMessageProfileKind, Version: 1,
		Caller:        orchestrationcommand.CallerSnapshot{Profile: principal.Profile, AuthContext: principal.AuthContext, ExpiresAt: principal.ExpiresAt},
		Authorization: authorizationDecision(request.Access, authorization),
		Source:        sourceSelector, Target: targetSelector,
		CompleteIntentDigest: zeroDigest(), DigestDomain: orchestrationcommand.SendMessageDigestDomain,
		DigestAlgorithm: orchestrationcommand.HMACSHA256DigestAlgorithm,
		DigestKeyID:     request.DigestKeyID, DigestKeyVersion: request.DigestKeyVersion,
	}, nil
}

func authorizationDecision(request accesswait.AccessRequest, authorization accesswait.Authorization) orchestrationcommand.AuthorizationDecision {
	return orchestrationcommand.AuthorizationDecision{
		Kind: orchestrationcommand.AccessGrantAuthorizationKind, Decision: "allowed", RequestVersion: 1,
		RequestID: authorization.RequestID, RequestDigest: request.RequestDigest,
		GrantID: authorization.GrantID, GrantRevision: authorization.GrantRevision, Capability: authorization.Capability,
		RequestedAt: request.RequestedAt, DecidedAt: authorization.AuthorizedAt, ExpiresAt: authorization.ExpiresAt,
		GrantRequestDigest: authorization.Grant.RequestDigest,
		GrantIssuedAt:      authorization.Grant.IssuedAt, GrantUpdatedAt: authorization.Grant.UpdatedAt,
	}
}

func selectorFromBinding(binding identity.CurrentBinding) (orchestrationcommand.ActiveBindingSelector, error) {
	updatedAt, err := time.Parse(time.RFC3339Nano, binding.UpdatedAt)
	if err != nil {
		return orchestrationcommand.ActiveBindingSelector{}, fmt.Errorf("parse identity binding timestamp: %w", err)
	}
	return orchestrationcommand.ActiveBindingSelector{
		Version: 1, Kind: orchestrationcommand.ActiveBindingSelectorKind,
		AgentID: binding.AgentID, IdentityKind: binding.IdentityKind,
		ReservationID: binding.ReservationID, ReservationGeneration: binding.ReservationGeneration,
		IncarnationID: binding.IncarnationID, IncarnationGeneration: binding.IncarnationGeneration,
		ExternalIdentityRef: binding.ExternalIdentityRef, BindingVersion: binding.BindingVersion,
		BindingDigest: binding.BindingDigest, FenceRef: binding.SourceFenceRef,
		ActiveProcessReceiptRef: binding.ActiveProcessReceiptRef, State: binding.State,
		BindingSchemaVersion: binding.SchemaVersion, UpdatedAt: updatedAt,
	}, nil
}

// bindingMatchesFence 比较ID current binding与grant中的完整代际fence。
func bindingMatchesFence(binding identity.CurrentBinding, fence accesswait.FenceSnapshot) bool {
	return binding.State == "active" && binding.AgentID == fence.AgentID && binding.ReservationID == fence.ReservationID &&
		binding.ReservationGeneration == fence.ReservationGeneration && binding.IncarnationID == fence.IncarnationID &&
		binding.IncarnationGeneration == fence.IncarnationGeneration && binding.BindingVersion == fence.BindingVersion &&
		binding.BindingDigest == fence.BindingDigest && binding.SourceFenceRef == fence.SourceFenceRef
}

func equalFence(left, right accesswait.FenceSnapshot) bool { return left == right }

func zeroDigest() string { return "0000000000000000000000000000000000000000000000000000000000000000" }
