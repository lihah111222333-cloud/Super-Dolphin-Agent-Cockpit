package accesswait

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// grantFromRow 将数据库授权行完整恢复为已校验的授权快照。
func grantFromRow(row storedb.OrchestrationAccessGrant) (AccessGrant, error) {
	issued, err := parseUTC("grant issued_at", row.IssuedAt)
	if err != nil {
		return AccessGrant{}, err
	}
	expires, err := parseUTC("grant expires_at", row.ExpiresAt)
	if err != nil {
		return AccessGrant{}, err
	}
	created, err := parseUTC("grant created_at", row.CreatedAt)
	if err != nil {
		return AccessGrant{}, err
	}
	updated, err := parseUTC("grant updated_at", row.UpdatedAt)
	if err != nil {
		return AccessGrant{}, err
	}
	source := FenceSnapshot{AgentID: row.SourceAgentID, ReservationID: row.SourceReservationID, ReservationGeneration: row.SourceReservationGeneration, IncarnationID: row.SourceIncarnationID, IncarnationGeneration: row.SourceIncarnationGeneration, BindingVersion: row.SourceBindingVersion, BindingDigest: row.SourceBindingDigest, SourceFenceRef: row.SourceFenceRef}
	target := FenceSnapshot{AgentID: row.TargetAgentID, ReservationID: row.TargetReservationID, ReservationGeneration: row.TargetReservationGeneration, IncarnationID: row.TargetIncarnationID, IncarnationGeneration: row.TargetIncarnationGeneration, BindingVersion: row.TargetBindingVersion, BindingDigest: row.TargetBindingDigest, SourceFenceRef: row.TargetFenceRef}
	grant := AccessGrant{GrantID: row.GrantID, CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef, Source: source, Target: target, CanRead: row.CanRead == 1, CanSend: row.CanSend == 1, IdempotencyNamespace: row.IdempotencyNamespace, IdempotencyKeyDigest: row.IdempotencyKeyDigest, RequestDigest: row.RequestDigest, IssuedAt: issued, ExpiresAt: expires, Revision: row.Revision, Status: row.Status, SchemaVersion: row.SchemaVersion, CreatedAt: created, UpdatedAt: updated}
	if row.Status == "revoked" {
		grant.RevokedAt = &updated
	}
	if row.Status == "expired" {
		grant.ExpiredAt = &updated
	}
	if err := grant.Validate(); err != nil {
		return AccessGrant{}, err
	}
	return grant, nil
}

// waitFromRow 解码并校验 durable wait 行，拒绝非规范 selector 摘要。
func waitFromRow(row storedb.OrchestrationEventWaitHandle) (WaitHandle, error) {
	deadline, err := parseUTC("wait deadline_at", row.DeadlineAt)
	if err != nil {
		return WaitHandle{}, err
	}
	created, err := parseUTC("wait created_at", row.CreatedAt)
	if err != nil {
		return WaitHandle{}, err
	}
	updated, err := parseUTC("wait updated_at", row.UpdatedAt)
	if err != nil {
		return WaitHandle{}, err
	}
	selector, err := selectorFromRow(row)
	if err != nil {
		return WaitHandle{}, err
	}
	wait := waitHandleFromRow(row, selector, deadline, created, updated)
	wait.ResolvedAt, err = optionalUTC("wait resolved_at", row.ResolvedAt)
	if err != nil {
		return WaitHandle{}, err
	}
	if err := wait.Validate(); err != nil {
		return WaitHandle{}, err
	}
	return wait, nil
}

// selectorFromRow 严格恢复 selector，并校验 JSON、列值和摘要三者一致。
func selectorFromRow(row storedb.OrchestrationEventWaitHandle) (EventSelector, error) {
	selector := EventSelector{}
	if err := decodeStrictJSON([]byte(row.SelectorJson), &selector); err != nil {
		return EventSelector{}, fmt.Errorf("decode wait selector: %w", err)
	}
	if err := selectorColumnsMatch(row, selector); err != nil {
		return EventSelector{}, err
	}
	var eventTypes []string
	if err := decodeStrictJSON([]byte(row.EventTypesJson), &eventTypes); err != nil {
		return EventSelector{}, fmt.Errorf("decode wait event types: %w", err)
	}
	selector.EventTypes = eventTypes
	_, _, expectedDigest, err := selectorJSON(selector)
	if err != nil {
		return EventSelector{}, err
	}
	if row.SelectorDigest != expectedDigest {
		return EventSelector{}, invalidWait("selector digest does not match canonical selector")
	}
	return selector, nil
}

// selectorColumnsMatch 拒绝 selector JSON 与其规范化列之间的不一致。
func selectorColumnsMatch(row storedb.OrchestrationEventWaitHandle, selector EventSelector) error {
	if selector.Version != row.SelectorVersion || selector.SourceID != row.SourceID || selector.SourceEpoch != row.SourceEpoch || selector.Lane != eventstore.Lane(row.Lane) || selector.TargetAgentID != row.TargetAgentID {
		return invalidWait("selector JSON does not match durable selector columns")
	}
	return nil
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func waitHandleFromRow(row storedb.OrchestrationEventWaitHandle, selector EventSelector, deadline, created, updated time.Time) WaitHandle {
	return WaitHandle{
		WaitHandleID: row.WaitHandleID, GrantID: row.GrantID, GrantRevision: row.GrantRevision,
		CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef, SourceAgentID: row.SourceAgentID, Selector: selector,
		AfterSequence: row.AfterSourceSequence, AfterCursor: row.AfterCursor, DeadlineAt: deadline,
		IdempotencyNamespace: row.IdempotencyNamespace, IdempotencyKeyDigest: row.IdempotencyKeyDigest,
		RequestDigest: row.RequestDigest, Status: row.Status, Revision: row.Revision,
		MatchedEventID: row.MatchedEventID, MatchedSourceSequence: row.MatchedSourceSequence,
		MatchedCanonicalCursor: row.MatchedCanonicalCursor, CreatedAt: created, UpdatedAt: updated,
		Target: FenceSnapshot{
			AgentID: row.TargetAgentID, ReservationID: row.TargetReservationID,
			ReservationGeneration: row.TargetReservationGeneration, IncarnationID: row.TargetIncarnationID,
			IncarnationGeneration: row.TargetIncarnationGeneration, BindingVersion: row.TargetBindingVersion,
			BindingDigest: row.TargetBindingDigest, SourceFenceRef: row.TargetFenceRef,
		},
	}
}

func optionalUTC(name, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := parseUTC(name, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func selectorJSON(selector EventSelector) (string, string, string, error) {
	if err := selector.Validate(); err != nil {
		return "", "", "", err
	}
	eventTypes := append([]string(nil), selector.EventTypes...)
	selectorCopy := selector
	selectorCopy.EventTypes = nil
	rawSelector, err := json.Marshal(selectorCopy)
	if err != nil {
		return "", "", "", fmt.Errorf("encode wait selector: %w", err)
	}
	rawTypes, err := json.Marshal(eventTypes)
	if err != nil {
		return "", "", "", fmt.Errorf("encode wait event types: %w", err)
	}
	selectorDigest, err := selector.Digest()
	if err != nil {
		return "", "", "", err
	}
	return string(rawSelector), string(rawTypes), selectorDigest, nil
}
