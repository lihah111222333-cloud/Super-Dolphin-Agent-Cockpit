package waitoperation

import (
	"encoding/json"
	"time"
)

func mutationRequestDigest(kind MutationKind, request any) (string, error) {
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", invalidOperation("encode commit marker request: %v", err)
	}
	return digest("wait-operation/commit-request/v1", append([]byte(string(kind)+"\x00"), encoded...)), nil
}

type operationMarkerDigestInput struct {
	OperationKey, OperationDigest, TargetSetDigest string
	CallerPrincipalRef, ScopeRef                   string
	Source                                         any
	IssuedAt, DeadlineAt, CreatedAt, UpdatedAt     string
	TargetCount, Revision                          int64
	Status, UncertainPhase                         string
	CleanupRequestDigest, CleanupReasonRef         string
	ResolvedAt                                     string
	SchemaVersion                                  int64
}

// operationStateDigest hashes all durable operation-head state fields.
func operationStateDigest(operation Operation) string {
	input := operationMarkerDigestInput{
		OperationKey: operation.OperationKey, OperationDigest: operation.OperationDigest, TargetSetDigest: operation.TargetSetDigest,
		CallerPrincipalRef: operation.CallerPrincipalRef, ScopeRef: operation.ScopeRef, Source: operation.Source,
		IssuedAt: formatUTC(operation.IssuedAt), DeadlineAt: formatUTC(operation.DeadlineAt), CreatedAt: formatUTC(operation.CreatedAt), UpdatedAt: formatUTC(operation.UpdatedAt),
		TargetCount: operation.TargetCount, Revision: operation.Revision, Status: operation.Status, UncertainPhase: operation.UncertainPhase,
		CleanupRequestDigest: operation.CleanupRequestDigest, CleanupReasonRef: operation.CleanupReasonRef,
		ResolvedAt: formatMarkerOptionalTime(operation.ResolvedAt), SchemaVersion: operation.SchemaVersion,
	}
	encoded, _ := json.Marshal(input)
	return digest("wait-operation/post-operation/v1", encoded)
}

type targetMarkerDigestInput struct {
	OperationKey, OperationDigest, Key                  string
	TargetIndex, Version                                int64
	TargetDigest, RecordDigest                          string
	PreviousRecordDigest                                string
	Target, Selector                                    any
	AfterSequence                                       int64
	AfterCursor, DeadlineAt                             string
	OriginalGrantID, GrantNamespace                     string
	GrantKeyDigest, GrantRequestDigest                  string
	ResolvedGrantID, GrantStatus                        string
	GrantRevision                                       int64
	WaitNamespace, WaitKeyDigest                        string
	WaitRequestDigest, ResolvedWaitHandleID, WaitStatus string
	WaitRevision                                        int64
	GrantCleanupState, WaitCleanupState                 string
	Phase, UncertainPhase, ErrorCode, ErrorRef          string
	ResolvedAt, RecordedAt                              string
	SchemaVersion                                       int64
}

// targetStateDigest hashes all durable target-head lifecycle and identity fields.
func targetStateDigest(target TargetState) string {
	input := targetMarkerDigestInput{
		OperationKey: target.OperationKey, OperationDigest: target.OperationDigest, Key: target.Key,
		TargetIndex: target.TargetIndex, Version: target.Version, TargetDigest: target.TargetDigest,
		RecordDigest: target.RecordDigest, PreviousRecordDigest: target.PreviousRecordDigest,
		Target: target.Target, Selector: target.Selector, AfterSequence: target.AfterSequence,
		AfterCursor: target.AfterCursor, DeadlineAt: formatUTC(target.DeadlineAt), OriginalGrantID: target.OriginalGrantID,
		GrantNamespace: target.GrantNamespace, GrantKeyDigest: target.GrantKeyDigest, GrantRequestDigest: target.GrantRequestDigest,
		ResolvedGrantID: markerOptionalString(target.ResolvedGrantID), GrantRevision: target.GrantRevision, GrantStatus: target.GrantStatus,
		WaitNamespace: target.WaitNamespace, WaitKeyDigest: target.WaitKeyDigest, WaitRequestDigest: target.WaitRequestDigest,
		ResolvedWaitHandleID: markerOptionalString(target.ResolvedWaitHandleID), WaitRevision: target.WaitRevision, WaitStatus: target.WaitStatus,
		GrantCleanupState: target.GrantCleanupState, WaitCleanupState: target.WaitCleanupState, Phase: target.Phase,
		UncertainPhase: target.UncertainPhase, ErrorCode: target.ErrorCode, ErrorRef: target.ErrorRef,
		ResolvedAt: formatMarkerOptionalTime(target.ResolvedAt), RecordedAt: formatUTC(target.RecordedAt), SchemaVersion: target.SchemaVersion,
	}
	encoded, _ := json.Marshal(input)
	return digest("wait-operation/post-target/v1", encoded)
}

func markerOptionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func formatMarkerOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatUTC(*value)
}
