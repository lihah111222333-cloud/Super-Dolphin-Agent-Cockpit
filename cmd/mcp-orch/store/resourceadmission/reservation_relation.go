package resourceadmission

import (
	"context"
	"database/sql"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type reservationAuthority struct {
	command storedb.OrchestrationCommandIntent
	profile storedb.OrchestrationCommandIntentProfile
	source  storedb.OrchestrationCurrentBinding
	target  storedb.OrchestrationCurrentBinding
	fence   storedb.OrchestrationCommandContentDeletionFence
	content storedb.OrchestrationContentRef
	object  storedb.OrchestrationContentObject
	ingest  storedb.OrchestrationContentIngestReservation
	policy  PolicySnapshot
	created time.Time
	expires time.Time
}

// loadReservationAuthority 在首个写入前重读command、双端ID、CONTENT和current policy。
func loadReservationAuthority(
	ctx context.Context,
	q *storedb.Queries,
	request ReserveRequest,
	now time.Time,
) (reservationAuthority, error) {
	command, profile, err := loadResourceCommandProfile(ctx, q, request)
	if err != nil {
		return reservationAuthority{}, err
	}
	source, target, err := loadResourceBindings(ctx, q, profile)
	if err != nil {
		return reservationAuthority{}, err
	}
	fence, ref, object, ingest, err := loadResourceContent(ctx, q, command, profile, now)
	if err != nil {
		return reservationAuthority{}, err
	}
	policy, err := loadCurrentResourcePolicy(ctx, q, command.CallerPrincipalRef, command.ScopeRef, now)
	if err != nil {
		return reservationAuthority{}, err
	}
	if !request.Requested.FitsWithin(policy.EffectiveLimits) {
		return reservationAuthority{}, ErrLimitExceeded
	}
	if request.Requested.ContentBytes != ref.SizeBytes || request.Requested.IngressBytes < ref.SizeBytes {
		return reservationAuthority{}, ErrLimitExceeded
	}
	expires, err := resourceReservationExpiry(policy.ValidUntil, profile, ref)
	if err != nil || !expires.After(now) {
		return reservationAuthority{}, ErrPolicyStale
	}
	return reservationAuthority{command: command, profile: profile, source: source, target: target,
		fence: fence, content: ref, object: object, ingest: ingest, policy: policy, created: now, expires: expires}, nil
}

// loadResourceCommandProfile 读取并验证send_message base/profile固定关系。
func loadResourceCommandProfile(
	ctx context.Context,
	q *storedb.Queries,
	request ReserveRequest,
) (storedb.OrchestrationCommandIntent, storedb.OrchestrationCommandIntentProfile, error) {
	command, err := q.GetOrchestrationCommandIntent(ctx, storedb.GetOrchestrationCommandIntentParams{CommandID: request.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.OrchestrationCommandIntent{}, storedb.OrchestrationCommandIntentProfile{}, ErrProfileUnavailable
	}
	if err != nil {
		return storedb.OrchestrationCommandIntent{}, storedb.OrchestrationCommandIntentProfile{}, err
	}
	profile, err := q.GetOrchestrationCommandIntentProfile(ctx, storedb.GetOrchestrationCommandIntentProfileParams{CommandID: request.CommandID})
	if errors.Is(err, sql.ErrNoRows) {
		return storedb.OrchestrationCommandIntent{}, storedb.OrchestrationCommandIntentProfile{}, ErrProfileUnavailable
	}
	if err != nil || !resourceCommandProfileMatches(command, profile, request) {
		return storedb.OrchestrationCommandIntent{}, storedb.OrchestrationCommandIntentProfile{}, errors.Join(ErrProfileMismatch, err)
	}
	return command, profile, nil
}

func resourceCommandProfileMatches(
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
	request ReserveRequest,
) bool {
	return resourceCommandMatchesRequest(command, request) && resourceProfileMatchesRequest(profile, command, request)
}

func resourceCommandMatchesRequest(command storedb.OrchestrationCommandIntent, request ReserveRequest) bool {
	return command.CommandID == request.CommandID && command.CommandVersion == request.CommandVersion &&
		command.CommandKind == RequestedKindSendMessage && command.RequestDigest == request.CommandProfileDigest
}

// resourceProfileMatchesRequest 校验profile类型、allow判决与完整digest。
func resourceProfileMatchesRequest(
	profile storedb.OrchestrationCommandIntentProfile,
	command storedb.OrchestrationCommandIntent,
	request ReserveRequest,
) bool {
	return profile.CommandID == command.CommandID && profile.ProfileKind == "send_message_v1" &&
		profile.ProfileVersion == 1 && profile.CompleteIntentDigest == request.CommandProfileDigest &&
		profile.AuthorizationDecision == "allowed" && profile.AuthorizationCapability == "send" &&
		profile.SchemaVersion == 1
}

func loadResourceBindings(
	ctx context.Context,
	q *storedb.Queries,
	profile storedb.OrchestrationCommandIntentProfile,
) (storedb.OrchestrationCurrentBinding, storedb.OrchestrationCurrentBinding, error) {
	source, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: profile.SourceAgentID})
	if err != nil {
		return storedb.OrchestrationCurrentBinding{}, storedb.OrchestrationCurrentBinding{}, errors.Join(ErrIdentityFence, err)
	}
	target, err := q.GetOrchestrationCurrentBinding(ctx, storedb.GetOrchestrationCurrentBindingParams{AgentID: profile.TargetAgentID})
	if err != nil || !resourceSourceBindingMatches(source, profile) || !resourceTargetBindingMatches(target, profile) {
		return storedb.OrchestrationCurrentBinding{}, storedb.OrchestrationCurrentBinding{}, errors.Join(ErrIdentityFence, err)
	}
	return source, target, nil
}

type expectedResourceBinding struct {
	agentID, reservationID, incarnationID, identityKind, externalIdentityRef string
	bindingDigest, fenceRef, processReceiptRef, updatedAt                    string
	bindingVersion, reservationGeneration, incarnationGeneration             int64
	schemaVersion                                                            int64
}

func resourceSourceBindingMatches(binding storedb.OrchestrationCurrentBinding, profile storedb.OrchestrationCommandIntentProfile) bool {
	return resourceBindingMatches(binding, expectedResourceBinding{
		agentID: profile.SourceAgentID, reservationID: profile.SourceReservationID,
		incarnationID: profile.SourceIncarnationID, identityKind: profile.SourceIdentityKind,
		externalIdentityRef: profile.SourceExternalIdentityRef, bindingDigest: profile.SourceBindingDigest,
		fenceRef: profile.SourceFenceRef, processReceiptRef: profile.SourceActiveProcessReceiptRef,
		updatedAt: profile.SourceBindingUpdatedAt, bindingVersion: profile.SourceBindingVersion,
		reservationGeneration: profile.SourceReservationGeneration,
		incarnationGeneration: profile.SourceIncarnationGeneration,
		schemaVersion:         profile.SourceBindingSchemaVersion,
	})
}

func resourceTargetBindingMatches(binding storedb.OrchestrationCurrentBinding, profile storedb.OrchestrationCommandIntentProfile) bool {
	return resourceBindingMatches(binding, expectedResourceBinding{
		agentID: profile.TargetAgentID, reservationID: profile.TargetReservationID,
		incarnationID: profile.TargetIncarnationID, identityKind: profile.TargetIdentityKind,
		externalIdentityRef: profile.TargetExternalIdentityRef, bindingDigest: profile.TargetBindingDigest,
		fenceRef: profile.TargetFenceRef, processReceiptRef: profile.TargetActiveProcessReceiptRef,
		updatedAt: profile.TargetBindingUpdatedAt, bindingVersion: profile.TargetBindingVersion,
		reservationGeneration: profile.TargetReservationGeneration,
		incarnationGeneration: profile.TargetIncarnationGeneration,
		schemaVersion:         profile.TargetBindingSchemaVersion,
	})
}

func resourceBindingMatches(binding storedb.OrchestrationCurrentBinding, expected expectedResourceBinding) bool {
	return resourceBindingIdentityMatches(binding, expected) && resourceBindingAuthorityMatches(binding, expected)
}

// resourceBindingIdentityMatches 校验ID-1 reservation/incarnation主体与generation。
func resourceBindingIdentityMatches(binding storedb.OrchestrationCurrentBinding, expected expectedResourceBinding) bool {
	return binding.AgentID == expected.agentID && binding.BindingVersion == expected.bindingVersion &&
		binding.ReservationID == expected.reservationID &&
		binding.ReservationGeneration == expected.reservationGeneration &&
		binding.IncarnationID == expected.incarnationID &&
		binding.IncarnationGeneration == expected.incarnationGeneration &&
		binding.IdentityKind == expected.identityKind && binding.ExternalIdentityRef == expected.externalIdentityRef
}

// resourceBindingAuthorityMatches 校验binding digest、fence、receipt、状态与时间。
func resourceBindingAuthorityMatches(binding storedb.OrchestrationCurrentBinding, expected expectedResourceBinding) bool {
	return binding.BindingDigest == expected.bindingDigest && binding.SourceFenceRef == expected.fenceRef &&
		binding.ActiveProcessReceiptRef == expected.processReceiptRef && binding.State == "active" &&
		binding.SchemaVersion == expected.schemaVersion && resourceBindingTimeMatches(binding.UpdatedAt, expected.updatedAt)
}

func resourceBindingTimeMatches(current, expected string) bool {
	currentTime, currentErr := time.Parse(time.RFC3339Nano, current)
	expectedTime, expectedErr := time.Parse(time.RFC3339Nano, expected)
	return currentErr == nil && expectedErr == nil && validTime(currentTime) && validTime(expectedTime) &&
		currentTime.Equal(expectedTime)
}

// loadResourceContent 读取并闭合command fence、CONTENT ref/object/ingest关系。
func loadResourceContent(
	ctx context.Context,
	q *storedb.Queries,
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
	now time.Time,
) (storedb.OrchestrationCommandContentDeletionFence, storedb.OrchestrationContentRef,
	storedb.OrchestrationContentObject, storedb.OrchestrationContentIngestReservation, error,
) {
	fence, err := q.GetCommandContentDeletionFence(ctx, storedb.GetCommandContentDeletionFenceParams{CommandID: command.CommandID})
	if err != nil {
		return storedb.OrchestrationCommandContentDeletionFence{}, storedb.OrchestrationContentRef{},
			storedb.OrchestrationContentObject{}, storedb.OrchestrationContentIngestReservation{}, errors.Join(ErrContentUnavailable, err)
	}
	ref, err := q.GetContentRefByCanonical(ctx, storedb.GetContentRefByCanonicalParams{CanonicalRef: command.RequestContentRef})
	if err != nil {
		return fence, storedb.OrchestrationContentRef{}, storedb.OrchestrationContentObject{},
			storedb.OrchestrationContentIngestReservation{}, errors.Join(ErrContentUnavailable, err)
	}
	object, err := q.GetContentObjectByReservation(ctx, storedb.GetContentObjectByReservationParams{IngestReservationID: ref.IngestReservationID})
	if err != nil {
		return fence, ref, storedb.OrchestrationContentObject{}, storedb.OrchestrationContentIngestReservation{},
			errors.Join(ErrContentUnavailable, err)
	}
	ingest, err := q.GetContentIngestReservationByID(ctx, storedb.GetContentIngestReservationByIDParams{IngestReservationID: ref.IngestReservationID})
	if err != nil || !resourceContentRowsMatch(fence, ref, object, ingest, command, profile, now) {
		return fence, ref, object, storedb.OrchestrationContentIngestReservation{}, errors.Join(ErrContentMismatch, err)
	}
	return fence, ref, object, ingest, nil
}

// resourceContentRowsMatch 串联proof有效期及fence/ref/object/ingest四组关系。
func resourceContentRowsMatch(
	fence storedb.OrchestrationCommandContentDeletionFence,
	ref storedb.OrchestrationContentRef,
	object storedb.OrchestrationContentObject,
	ingest storedb.OrchestrationContentIngestReservation,
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
	now time.Time,
) bool {
	proofExpires, err := time.Parse(time.RFC3339Nano, ref.ProofExpiresAt)
	return err == nil && proofExpires.After(now) && resourceContentFenceMatches(fence, ref, command, profile) &&
		resourceContentRefMatches(ref, command, profile) && resourceContentObjectMatches(object, ref) &&
		resourceContentIngestMatches(ingest, ref, command, profile)
}

// resourceContentFenceMatches 校验command deletion fence精确绑定live ref。
func resourceContentFenceMatches(
	fence storedb.OrchestrationCommandContentDeletionFence,
	ref storedb.OrchestrationContentRef,
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
) bool {
	return fence.CommandID == command.CommandID && fence.CommandProfileDigest == profile.CompleteIntentDigest &&
		fence.FenceKind == "non_terminal_command" && fence.FenceState == "held" &&
		fence.CanonicalRef == ref.CanonicalRef && fence.ReferenceID == ref.ReferenceID &&
		fence.ObjectID == ref.ObjectID && fence.ObjectVersion == ref.ObjectVersion && fence.RefVersion == ref.RefVersion
}

// resourceContentRefMatches 校验canonical ref与command principal/source关系。
func resourceContentRefMatches(
	ref storedb.OrchestrationContentRef,
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
) bool {
	return ref.CanonicalRef == command.RequestContentRef && ref.ReferenceState == "live" &&
		ref.CallerPrincipalRef == command.CallerPrincipalRef && ref.ScopeRef == command.ScopeRef &&
		ref.SourceAgentID == profile.SourceAgentID && ref.SourceFenceRef == profile.SourceFenceRef
}

func resourceContentObjectMatches(object storedb.OrchestrationContentObject, ref storedb.OrchestrationContentRef) bool {
	return object.ObjectID == ref.ObjectID && object.ObjectVersion == ref.ObjectVersion &&
		object.IngestReservationID == ref.IngestReservationID && object.ContentState == "active" && object.KeyState == "active"
}

// resourceContentIngestMatches 校验promoted ingest与command/source关系。
func resourceContentIngestMatches(
	ingest storedb.OrchestrationContentIngestReservation,
	ref storedb.OrchestrationContentRef,
	command storedb.OrchestrationCommandIntent,
	profile storedb.OrchestrationCommandIntentProfile,
) bool {
	return ingest.IngestReservationID == ref.IngestReservationID && ingest.State == "promoted" &&
		ingest.CallerPrincipalRef == command.CallerPrincipalRef && ingest.ScopeRef == command.ScopeRef &&
		ingest.SourceAgentID == profile.SourceAgentID && ingest.SourceFenceRef == profile.SourceFenceRef
}

// loadCurrentResourcePolicy 选择当前最高eligible policy且拒绝歧义或损坏。
func loadCurrentResourcePolicy(
	ctx context.Context,
	q *storedb.Queries,
	principal string,
	scope string,
	now time.Time,
) (PolicySnapshot, error) {
	observedAt := now.Format(time.RFC3339Nano)
	rows, err := q.ListCurrentResourceEffectivePolicies(ctx, storedb.ListCurrentResourceEffectivePoliciesParams{
		CallerPrincipalRef: principal, ScopeRef: scope,
		ObservedAtEffective: observedAt, ObservedAtExpiry: observedAt,
	})
	if err != nil || len(rows) == 0 {
		return PolicySnapshot{}, errors.Join(ErrPolicyUnavailable, err)
	}
	policy, err := policyFromRow(rows[0])
	if err != nil || !policy.ValidUntil.After(now) || policy.EffectiveAt.After(now) {
		return PolicySnapshot{}, errors.Join(ErrPolicyStale, err)
	}
	return policy, nil
}

func resourceReservationExpiry(
	policyExpiry time.Time,
	profile storedb.OrchestrationCommandIntentProfile,
	content storedb.OrchestrationContentRef,
) (time.Time, error) {
	values := []string{profile.CallerAuthExpiresAt, profile.AuthorizationExpiresAt, content.ProofExpiresAt}
	result := policyExpiry
	for _, value := range values {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || !validTime(parsed) {
			return time.Time{}, ErrProfileMismatch
		}
		if parsed.Before(result) {
			result = parsed
		}
	}
	return result, nil
}
