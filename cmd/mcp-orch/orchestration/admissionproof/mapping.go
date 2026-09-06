package admissionproof

import (
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

func contentResolveKey(record orchestrationcommand.CompleteIntentRecord) ContentResolveKey {
	return ContentResolveKey{
		SchemaVersion: 1, CommandID: record.Command.Intent.CommandID,
		CommandVersion: record.Command.CommandVersion, CommandProfileDigest: record.Profile.CompleteIntentDigest,
		CallerPrincipalRef: record.Command.Intent.CallerPrincipalRef, ScopeRef: record.Command.Intent.ScopeRef,
		SourceAgentID: record.Profile.Source.AgentID, SourceFenceRef: record.Profile.Source.FenceRef,
	}
}

func resourceResolveKey(record orchestrationcommand.CompleteIntentRecord, identityDigest, contentDigest string) ResourceResolveKey {
	return ResourceResolveKey{
		SchemaVersion: 1, CommandID: record.Command.Intent.CommandID,
		CommandVersion: record.Command.CommandVersion, CommandProfileDigest: record.Profile.CompleteIntentDigest,
		CallerPrincipalRef: record.Command.Intent.CallerPrincipalRef, ScopeRef: record.Command.Intent.ScopeRef,
		IdentityFenceDigest: identityDigest, ContentSnapshotDigest: contentDigest,
	}
}

// contentMatchesRecord 比较CONTENT proof与持久command/profile的全部join字段。
func contentMatchesRecord(key ContentResolveKey, snapshot ContentSnapshot, record orchestrationcommand.CompleteIntentRecord) bool {
	return snapshot.CommandID == key.CommandID && snapshot.CommandVersion == key.CommandVersion &&
		snapshot.CommandProfileDigest == key.CommandProfileDigest && snapshot.CallerPrincipalRef == key.CallerPrincipalRef &&
		snapshot.ScopeRef == key.ScopeRef && snapshot.SourceAgentID == key.SourceAgentID &&
		snapshot.SourceFenceRef == key.SourceFenceRef &&
		snapshot.ContentRef == record.Command.Intent.RequestContentRef && proofWindowWithinAuthorization(snapshot.ValidUntil, record)
}

// resourceMatchesRecord 比较RESOURCE proof与command/identity/content链的全部join字段。
func resourceMatchesRecord(key ResourceResolveKey, snapshot ResourceSnapshot, record orchestrationcommand.CompleteIntentRecord) bool {
	return snapshot.CommandID == key.CommandID && snapshot.CommandVersion == key.CommandVersion &&
		snapshot.CommandProfileDigest == key.CommandProfileDigest && snapshot.CallerPrincipalRef == key.CallerPrincipalRef &&
		snapshot.ScopeRef == key.ScopeRef && snapshot.IdentityFenceDigest == key.IdentityFenceDigest &&
		snapshot.ContentSnapshotDigest == key.ContentSnapshotDigest && snapshot.RequestedKind == record.Command.Intent.CommandKind &&
		proofWindowWithinAuthorization(snapshot.ValidUntil, record)
}

func proofWindowWithinAuthorization(validUntil time.Time, record orchestrationcommand.CompleteIntentRecord) bool {
	authorization := record.Profile.Authorization
	return validUntil.After(authorization.DecidedAt) && !validUntil.After(authorization.ExpiresAt) &&
		!validUntil.After(record.Profile.Caller.ExpiresAt)
}

func buildCustodyRequest(config Config, record orchestrationcommand.CompleteIntentRecord, authorizationDigest, identityDigest, contentDigest, resourceDigest string) (recoverycustody.SubmissionRequest, error) {
	operationID, err := computeOperationID(record.Command.Intent.CommandID, record.Command.CommandVersion, record.Profile.CompleteIntentDigest)
	if err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	batchInput := batchDigestInput{
		CommandID: record.Command.Intent.CommandID, CommandVersion: record.Command.CommandVersion,
		CommandProfileDigest: record.Profile.CompleteIntentDigest, AuthorizationDigest: authorizationDigest,
		IdentityFenceDigest: identityDigest, ContentSnapshotDigest: contentDigest,
		ResourceSnapshotDigest: resourceDigest, IdempotencyKeyDigest: record.Command.Intent.IdempotencyKeyDigest,
	}
	batchDigest, err := computeBatchDigest(batchInput)
	if err != nil {
		return recoverycustody.SubmissionRequest{}, err
	}
	request := recoverycustody.SubmissionRequest{
		SchemaVersion: 1, OperationID: operationID, SecurityDomainID: config.SecurityDomainID,
		AuthorityProfile: config.AuthorityProfile, CommandID: record.Command.Intent.CommandID,
		CommandVersion: record.Command.CommandVersion, CommandProfileDigest: record.Profile.CompleteIntentDigest,
		AuthorizationDigest: authorizationDigest, IdentityFenceDigest: identityDigest,
		ContentSnapshotDigest: contentDigest, ResourceSnapshotDigest: resourceDigest,
		BatchDigest: batchDigest, IdempotencyKeyDigest: record.Command.Intent.IdempotencyKeyDigest,
	}
	return recoverycustody.FinalizeRequestDigest(request)
}
