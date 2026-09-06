package orchestrationcommand

import (
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func sendProfileParams(commandID string, profile SendMessageProfile) storedb.CreateOrchestrationCommandIntentProfileParams {
	return storedb.CreateOrchestrationCommandIntentProfileParams{
		CommandID: commandID, ProfileKind: profile.Kind,
		CallerProfile: profile.Caller.Profile, CallerAuthContext: profile.Caller.AuthContext,
		CallerAuthExpiresAt: profileTime(profile.Caller.ExpiresAt),
		AuthorizationKind:   profile.Authorization.Kind, AuthorizationDecision: profile.Authorization.Decision,
		AuthorizationRequestID: profile.Authorization.RequestID, AuthorizationRequestDigest: profile.Authorization.RequestDigest,
		AuthorizationGrantID: profile.Authorization.GrantID, AuthorizationGrantRevision: profile.Authorization.GrantRevision,
		AuthorizationCapability: profile.Authorization.Capability, AuthorizationRequestedAt: profileTime(profile.Authorization.RequestedAt),
		AuthorizationDecidedAt: profileTime(profile.Authorization.DecidedAt), AuthorizationExpiresAt: profileTime(profile.Authorization.ExpiresAt),
		GrantRequestDigest: profile.Authorization.GrantRequestDigest, GrantIssuedAt: profileTime(profile.Authorization.GrantIssuedAt),
		GrantUpdatedAt:     profileTime(profile.Authorization.GrantUpdatedAt),
		SourceSelectorKind: profile.Source.Kind, SourceAgentID: profile.Source.AgentID, SourceIdentityKind: profile.Source.IdentityKind,
		SourceReservationID: profile.Source.ReservationID, SourceReservationGeneration: profile.Source.ReservationGeneration,
		SourceIncarnationID: profile.Source.IncarnationID, SourceIncarnationGeneration: profile.Source.IncarnationGeneration,
		SourceExternalIdentityRef: profile.Source.ExternalIdentityRef, SourceBindingVersion: profile.Source.BindingVersion,
		SourceBindingDigest: profile.Source.BindingDigest, SourceFenceRef: profile.Source.FenceRef,
		SourceActiveProcessReceiptRef: profile.Source.ActiveProcessReceiptRef, SourceBindingState: profile.Source.State,
		SourceBindingUpdatedAt: profileTime(profile.Source.UpdatedAt),
		TargetSelectorKind:     profile.Target.Kind, TargetAgentID: profile.Target.AgentID, TargetIdentityKind: profile.Target.IdentityKind,
		TargetReservationID: profile.Target.ReservationID, TargetReservationGeneration: profile.Target.ReservationGeneration,
		TargetIncarnationID: profile.Target.IncarnationID, TargetIncarnationGeneration: profile.Target.IncarnationGeneration,
		TargetExternalIdentityRef: profile.Target.ExternalIdentityRef, TargetBindingVersion: profile.Target.BindingVersion,
		TargetBindingDigest: profile.Target.BindingDigest, TargetFenceRef: profile.Target.FenceRef,
		TargetActiveProcessReceiptRef: profile.Target.ActiveProcessReceiptRef, TargetBindingState: profile.Target.State,
		TargetBindingUpdatedAt: profileTime(profile.Target.UpdatedAt),
		CompleteIntentDigest:   profile.CompleteIntentDigest, DigestDomain: profile.DigestDomain,
		DigestAlgorithm: profile.DigestAlgorithm, DigestKeyID: profile.DigestKeyID, DigestKeyVersion: profile.DigestKeyVersion,
	}
}

func sendProfileFromRow(row storedb.OrchestrationCommandIntentProfile) (SendMessageProfile, time.Time, error) {
	callerExpiresAt, err := parseProfileTimestamp(row.CallerAuthExpiresAt)
	if err != nil {
		return SendMessageProfile{}, time.Time{}, err
	}
	authorizationTimes, err := parseAuthorizationTimes(row)
	if err != nil {
		return SendMessageProfile{}, time.Time{}, err
	}
	sourceUpdatedAt, err := parseProfileTimestamp(row.SourceBindingUpdatedAt)
	if err != nil {
		return SendMessageProfile{}, time.Time{}, err
	}
	targetUpdatedAt, err := parseProfileTimestamp(row.TargetBindingUpdatedAt)
	if err != nil {
		return SendMessageProfile{}, time.Time{}, err
	}
	recordedAt, err := parseProfileTimestamp(row.RecordedAt)
	if err != nil {
		return SendMessageProfile{}, time.Time{}, err
	}
	profile := SendMessageProfile{
		Kind: row.ProfileKind, Version: row.ProfileVersion,
		Caller:        CallerSnapshot{Profile: row.CallerProfile, AuthContext: row.CallerAuthContext, ExpiresAt: callerExpiresAt},
		Authorization: authorizationFromRow(row, authorizationTimes),
		Source:        selectorFromSourceRow(row, sourceUpdatedAt), Target: selectorFromTargetRow(row, targetUpdatedAt),
		CompleteIntentDigest: row.CompleteIntentDigest, DigestDomain: row.DigestDomain,
		DigestAlgorithm: row.DigestAlgorithm, DigestKeyID: row.DigestKeyID, DigestKeyVersion: row.DigestKeyVersion,
	}
	return profile, recordedAt, nil
}

type authorizationTimes struct {
	requestedAt, decidedAt, expiresAt, grantIssuedAt, grantUpdatedAt time.Time
}

func parseAuthorizationTimes(row storedb.OrchestrationCommandIntentProfile) (authorizationTimes, error) {
	values := []*time.Time{}
	times := authorizationTimes{}
	values = append(values, &times.requestedAt, &times.decidedAt, &times.expiresAt, &times.grantIssuedAt, &times.grantUpdatedAt)
	inputs := []string{row.AuthorizationRequestedAt, row.AuthorizationDecidedAt, row.AuthorizationExpiresAt, row.GrantIssuedAt, row.GrantUpdatedAt}
	for index, input := range inputs {
		parsed, err := parseProfileTimestamp(input)
		if err != nil {
			return authorizationTimes{}, err
		}
		*values[index] = parsed
	}
	return times, nil
}

func authorizationFromRow(row storedb.OrchestrationCommandIntentProfile, times authorizationTimes) AuthorizationDecision {
	return AuthorizationDecision{
		Kind: row.AuthorizationKind, Decision: row.AuthorizationDecision, RequestVersion: row.AuthorizationRequestVersion,
		RequestID: row.AuthorizationRequestID, RequestDigest: row.AuthorizationRequestDigest,
		GrantID: row.AuthorizationGrantID, GrantRevision: row.AuthorizationGrantRevision, Capability: row.AuthorizationCapability,
		RequestedAt: times.requestedAt, DecidedAt: times.decidedAt, ExpiresAt: times.expiresAt,
		GrantRequestDigest: row.GrantRequestDigest, GrantIssuedAt: times.grantIssuedAt, GrantUpdatedAt: times.grantUpdatedAt,
	}
}

func selectorFromSourceRow(row storedb.OrchestrationCommandIntentProfile, updatedAt time.Time) ActiveBindingSelector {
	return ActiveBindingSelector{
		Version: row.SourceSelectorVersion, Kind: row.SourceSelectorKind, AgentID: row.SourceAgentID, IdentityKind: row.SourceIdentityKind,
		ReservationID: row.SourceReservationID, ReservationGeneration: row.SourceReservationGeneration,
		IncarnationID: row.SourceIncarnationID, IncarnationGeneration: row.SourceIncarnationGeneration,
		ExternalIdentityRef: row.SourceExternalIdentityRef, BindingVersion: row.SourceBindingVersion,
		BindingDigest: row.SourceBindingDigest, FenceRef: row.SourceFenceRef,
		ActiveProcessReceiptRef: row.SourceActiveProcessReceiptRef, State: row.SourceBindingState,
		BindingSchemaVersion: row.SourceBindingSchemaVersion, UpdatedAt: updatedAt,
	}
}

func selectorFromTargetRow(row storedb.OrchestrationCommandIntentProfile, updatedAt time.Time) ActiveBindingSelector {
	return ActiveBindingSelector{
		Version: row.TargetSelectorVersion, Kind: row.TargetSelectorKind, AgentID: row.TargetAgentID, IdentityKind: row.TargetIdentityKind,
		ReservationID: row.TargetReservationID, ReservationGeneration: row.TargetReservationGeneration,
		IncarnationID: row.TargetIncarnationID, IncarnationGeneration: row.TargetIncarnationGeneration,
		ExternalIdentityRef: row.TargetExternalIdentityRef, BindingVersion: row.TargetBindingVersion,
		BindingDigest: row.TargetBindingDigest, FenceRef: row.TargetFenceRef,
		ActiveProcessReceiptRef: row.TargetActiveProcessReceiptRef, State: row.TargetBindingState,
		BindingSchemaVersion: row.TargetBindingSchemaVersion, UpdatedAt: updatedAt,
	}
}

func parseProfileTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || !validUTCTime(parsed) {
		return time.Time{}, errors.New("command intent profile timestamp is invalid")
	}
	return parsed, nil
}
