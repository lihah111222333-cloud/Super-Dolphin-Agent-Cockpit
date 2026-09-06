package waitoperation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

type targetDigestInput struct {
	Key           string                   `json:"key"`
	Target        accesswait.FenceSnapshot `json:"target"`
	Selector      accesswait.EventSelector `json:"selector"`
	AfterSequence int64                    `json:"after_sequence"`
	AfterCursor   string                   `json:"after_cursor"`
}

type targetTransitionDigestInput struct {
	OperationKey, OperationDigest, TargetKey string
	TargetIndex, Version                     int64
	TargetDigest                             string
	TargetAgentID, TargetReservationID       string
	TargetReservationGeneration              int64
	TargetIncarnationID                      string
	TargetIncarnationGeneration              int64
	TargetBindingVersion                     int64
	TargetBindingDigest, TargetFenceRef      string
	SourceID, SourceEpoch, Lane              string
	SelectorVersion                          int64
	SelectorJSON, SelectorDigest             string
	EventTypesJSON                           string
	AfterSourceSequence                      int64
	AfterCursor, DeadlineAt                  string
	OriginalGrantID, GrantNamespace          string
	GrantKeyDigest, GrantRequestDigest       string
	WaitNamespace, WaitKeyDigest             string
	WaitRequestDigest                        string
	PreviousRecordDigest                     string
	ResolvedGrantID                          *string
	GrantRevision                            int64
	GrantStatus                              string
	ResolvedWaitHandleID                     *string
	WaitRevision                             int64
	WaitStatus                               string
	GrantCleanupState, WaitCleanupState      string
	NextPhase, UncertainPhase                string
	ErrorCode, ErrorRef, ResolvedAt          string
	RecordedAt                               string
}

// deriveTargets produces all SQL parameters from one canonical target list.
// IDs, namespaces and request digests are deterministic opaque references, so
// retry candidates cannot silently create another grant/wait idempotency key.
func deriveTargets(request PlanRequest) ([]storedb.CreateOrchestrationWaitTargetPlanParams, string, error) {
	targetSetDigest, err := deriveTargetSetDigest(request.Targets)
	if err != nil {
		return nil, "", err
	}
	derived := make([]storedb.CreateOrchestrationWaitTargetPlanParams, 0, len(request.Targets))
	for index, candidate := range request.Targets {
		plan, err := deriveTargetPlan(request, candidate, index)
		if err != nil {
			return nil, "", err
		}
		derived = append(derived, plan)
	}
	return derived, targetSetDigest, nil
}

func canonicalPlanMarkerRequest(request PlanRequest, derived []storedb.CreateOrchestrationWaitTargetPlanParams) PlanRequest {
	normalized := request
	normalized.Targets = make([]TargetPlan, len(request.Targets))
	copy(normalized.Targets, request.Targets)
	for index := range normalized.Targets {
		if index >= len(derived) {
			break
		}
		candidate := &normalized.Targets[index]
		plan := derived[index]
		candidate.OriginalGrantID = plan.OriginalGrantID
		candidate.GrantNamespace = plan.GrantNamespace
		candidate.GrantKeyDigest = plan.GrantKeyDigest
		candidate.GrantRequestDigest = plan.GrantRequestDigest
		candidate.WaitNamespace = plan.WaitNamespace
		candidate.WaitKeyDigest = plan.WaitKeyDigest
		candidate.WaitRequestDigest = plan.WaitRequestDigest
	}
	return normalized
}

func deriveTargetSetDigest(targets []TargetPlan) (string, error) {
	base := make([]targetDigestInput, 0, len(targets))
	for _, target := range targets {
		base = append(base, targetDigestInput{Key: target.Key, Target: target.Target, Selector: target.Selector, AfterSequence: target.AfterSequence, AfterCursor: target.AfterCursor})
	}
	encoded, err := json.Marshal(base)
	if err != nil {
		return "", invalidOperation("encode target set: %v", err)
	}
	return digest("wait-operation/target-set/v1", encoded), nil
}

type targetReferences struct {
	originalGrantID, grantNamespace, grantKeyDigest, grantRequestDigest string
	waitNamespace, waitKeyDigest, waitRequestDigest                     string
}

// deriveTargetPlan 推导单个目标的规范 SQL 参数和幂等引用。
func deriveTargetPlan(request PlanRequest, candidate TargetPlan, index int) (storedb.CreateOrchestrationWaitTargetPlanParams, error) {
	selectorJSON, err := selectorStorageJSON(candidate.Selector)
	if err != nil {
		return storedb.CreateOrchestrationWaitTargetPlanParams{}, invalidOperation("target %q selector JSON: %v", candidate.Key, err)
	}
	selectorDigest, err := candidate.Selector.Digest()
	if err != nil {
		return storedb.CreateOrchestrationWaitTargetPlanParams{}, invalidOperation("target %q selector digest: %v", candidate.Key, err)
	}
	eventTypesJSON, err := json.Marshal(candidate.Selector.EventTypes)
	if err != nil {
		return storedb.CreateOrchestrationWaitTargetPlanParams{}, invalidOperation("target %q event types JSON: %v", candidate.Key, err)
	}
	targetDigest, err := computeTargetDigest(candidate.Key, candidate.Target, candidate.Selector, candidate.AfterSequence, candidate.AfterCursor)
	if err != nil {
		return storedb.CreateOrchestrationWaitTargetPlanParams{}, invalidOperation("target %q digest input: %v", candidate.Key, err)
	}
	refs := deriveTargetReferences(request, candidate, targetDigest)
	if err := validateSuppliedReferences(candidate, refs); err != nil {
		return storedb.CreateOrchestrationWaitTargetPlanParams{}, err
	}
	recordDigest := digest("wait-operation/target-record/v1", []byte(request.OperationKey+"\x00"+request.OperationDigest+"\x00"+targetDigest))
	return storedb.CreateOrchestrationWaitTargetPlanParams{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: candidate.Key, TargetIndex: int64(index + 1),
		TargetDigest: targetDigest, RecordDigest: recordDigest,
		TargetAgentID: candidate.Target.AgentID, TargetReservationID: candidate.Target.ReservationID, TargetReservationGeneration: candidate.Target.ReservationGeneration,
		TargetIncarnationID: candidate.Target.IncarnationID, TargetIncarnationGeneration: candidate.Target.IncarnationGeneration,
		TargetBindingVersion: candidate.Target.BindingVersion, TargetBindingDigest: candidate.Target.BindingDigest, TargetFenceRef: candidate.Target.SourceFenceRef,
		SourceID: candidate.Selector.SourceID, SourceEpoch: candidate.Selector.SourceEpoch, Lane: string(candidate.Selector.Lane), SelectorVersion: candidate.Selector.Version,
		SelectorJson: string(selectorJSON), SelectorDigest: selectorDigest, EventTypesJson: string(eventTypesJSON),
		AfterSourceSequence: candidate.AfterSequence, AfterCursor: candidate.AfterCursor, DeadlineAt: formatUTC(request.DeadlineAt),
		OriginalGrantID: refs.originalGrantID, GrantNamespace: refs.grantNamespace, GrantKeyDigest: refs.grantKeyDigest, GrantRequestDigest: refs.grantRequestDigest,
		WaitNamespace: refs.waitNamespace, WaitKeyDigest: refs.waitKeyDigest, WaitRequestDigest: refs.waitRequestDigest, RecordedAt: "",
	}, nil
}

func deriveTargetReferences(request PlanRequest, candidate TargetPlan, targetDigest string) targetReferences {
	identityKey := []byte(request.OperationKey + "\x00" + request.OperationDigest + "\x00" + candidate.Key)
	return targetReferences{
		originalGrantID:    "grant:" + digest("wait-operation/grant-id/v1", identityKey),
		grantNamespace:     "wait-operation/grant/" + digest("wait-operation/grant-namespace/v1", identityKey)[:32],
		grantKeyDigest:     digest("wait-operation/grant-key/v1", identityKey),
		grantRequestDigest: digest("wait-operation/grant-request/v1", []byte(request.CallerPrincipalRef+"\x00"+request.ScopeRef+"\x00"+request.OperationDigest+"\x00"+targetDigest)),
		waitNamespace:      "wait-operation/wait/" + digest("wait-operation/wait-namespace/v1", identityKey)[:32],
		waitKeyDigest:      digest("wait-operation/wait-key/v1", identityKey),
		waitRequestDigest:  digest("wait-operation/wait-request/v1", []byte(request.CallerPrincipalRef+"\x00"+request.ScopeRef+"\x00"+request.OperationDigest+"\x00"+targetDigest+"\x00"+fmt.Sprint(candidate.AfterSequence)+"\x00"+candidate.AfterCursor)),
	}
}

func validateSuppliedReferences(candidate TargetPlan, refs targetReferences) error {
	values := []struct{ name, supplied, canonical string }{
		{"original_grant_id", candidate.OriginalGrantID, refs.originalGrantID}, {"grant_namespace", candidate.GrantNamespace, refs.grantNamespace},
		{"grant_key_digest", candidate.GrantKeyDigest, refs.grantKeyDigest}, {"grant_request_digest", candidate.GrantRequestDigest, refs.grantRequestDigest},
		{"wait_namespace", candidate.WaitNamespace, refs.waitNamespace}, {"wait_key_digest", candidate.WaitKeyDigest, refs.waitKeyDigest}, {"wait_request_digest", candidate.WaitRequestDigest, refs.waitRequestDigest},
	}
	for _, value := range values {
		if value.supplied != "" && value.supplied != value.canonical {
			return invalidOperation("%s does not match canonical target reference", value.name)
		}
	}
	return nil
}

func targetPlanMatchesRow(expected storedb.CreateOrchestrationWaitTargetPlanParams, row storedb.OrchestrationWaitOperationTargetJournal) bool {
	actual := storedb.CreateOrchestrationWaitTargetPlanParams{
		OperationKey: row.OperationKey, OperationDigest: row.OperationDigest, TargetKey: row.TargetKey, TargetIndex: row.TargetIndex,
		TargetDigest: row.TargetDigest, TargetAgentID: row.TargetAgentID, TargetReservationID: row.TargetReservationID,
		TargetReservationGeneration: row.TargetReservationGeneration, TargetIncarnationID: row.TargetIncarnationID,
		TargetIncarnationGeneration: row.TargetIncarnationGeneration, TargetBindingVersion: row.TargetBindingVersion,
		TargetBindingDigest: row.TargetBindingDigest, TargetFenceRef: row.TargetFenceRef, SourceID: row.SourceID,
		SourceEpoch: row.SourceEpoch, Lane: row.Lane, SelectorVersion: row.SelectorVersion, SelectorJson: row.SelectorJson,
		SelectorDigest: row.SelectorDigest, EventTypesJson: row.EventTypesJson, AfterSourceSequence: row.AfterSourceSequence,
		AfterCursor: row.AfterCursor, DeadlineAt: row.DeadlineAt, OriginalGrantID: row.OriginalGrantID,
		GrantNamespace: row.GrantNamespace, GrantKeyDigest: row.GrantKeyDigest, GrantRequestDigest: row.GrantRequestDigest,
		WaitNamespace: row.WaitNamespace, WaitKeyDigest: row.WaitKeyDigest, WaitRequestDigest: row.WaitRequestDigest,
	}
	expected.RecordedAt = ""
	expected.RecordDigest = ""
	return reflect.DeepEqual(expected, actual)
}

func computeTargetDigest(key string, target accesswait.FenceSnapshot, selector accesswait.EventSelector, afterSequence int64, afterCursor string) (string, error) {
	encoded, err := json.Marshal(targetDigestInput{Key: key, Target: target, Selector: selector, AfterSequence: afterSequence, AfterCursor: afterCursor})
	if err != nil {
		return "", err
	}
	return digest("wait-operation/target/v1", encoded), nil
}

func initialTargetRecordDigest(operationKey, operationDigest, targetDigest string) string {
	return digest("wait-operation/target-record/v1", []byte(operationKey+"\x00"+operationDigest+"\x00"+targetDigest))
}

func targetSetDigestFromStates(states []TargetState) (string, error) {
	inputs := make([]targetDigestInput, 0, len(states))
	for _, state := range states {
		inputs = append(inputs, targetDigestInput{Key: state.Key, Target: state.Target, Selector: state.Selector, AfterSequence: state.AfterSequence, AfterCursor: state.AfterCursor})
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return "", err
	}
	return digest("wait-operation/target-set/v1", encoded), nil
}

func computeTargetRecordDigest(current storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest, recordedAt, resolvedAt string) (string, error) {
	input := targetTransitionDigestInput{
		OperationKey: current.OperationKey, OperationDigest: current.OperationDigest, TargetKey: current.TargetKey,
		TargetIndex: current.TargetIndex, Version: current.Version + 1, TargetDigest: current.TargetDigest,
		TargetAgentID: current.TargetAgentID, TargetReservationID: current.TargetReservationID,
		TargetReservationGeneration: current.TargetReservationGeneration, TargetIncarnationID: current.TargetIncarnationID,
		TargetIncarnationGeneration: current.TargetIncarnationGeneration, TargetBindingVersion: current.TargetBindingVersion,
		TargetBindingDigest: current.TargetBindingDigest, TargetFenceRef: current.TargetFenceRef,
		SourceID: current.SourceID, SourceEpoch: current.SourceEpoch, Lane: current.Lane,
		SelectorVersion: current.SelectorVersion, SelectorJSON: current.SelectorJson, SelectorDigest: current.SelectorDigest,
		EventTypesJSON: current.EventTypesJson, AfterSourceSequence: current.AfterSourceSequence, AfterCursor: current.AfterCursor,
		DeadlineAt: current.DeadlineAt, OriginalGrantID: current.OriginalGrantID, GrantNamespace: current.GrantNamespace,
		GrantKeyDigest: current.GrantKeyDigest, GrantRequestDigest: current.GrantRequestDigest,
		WaitNamespace: current.WaitNamespace, WaitKeyDigest: current.WaitKeyDigest, WaitRequestDigest: current.WaitRequestDigest,
		PreviousRecordDigest: current.RecordDigest, ResolvedGrantID: cloneString(request.ResolvedGrantID), GrantRevision: request.GrantRevision,
		GrantStatus: request.GrantStatus, ResolvedWaitHandleID: cloneString(request.ResolvedWaitHandleID), WaitRevision: request.WaitRevision,
		WaitStatus: request.WaitStatus, GrantCleanupState: request.GrantCleanupState, WaitCleanupState: request.WaitCleanupState,
		NextPhase: request.NextPhase, UncertainPhase: request.UncertainPhase, ErrorCode: request.ErrorCode,
		ErrorRef: request.ErrorRef, ResolvedAt: resolvedAt, RecordedAt: recordedAt,
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", invalidOperation("encode target transition: %v", err)
	}
	return digest("wait-operation/target-transition/v1", encoded), nil
}

func digest(domain string, payload []byte) string {
	hash := sha256.Sum256(append([]byte(domain+"\x00"), payload...))
	return hex.EncodeToString(hash[:])
}
