package accesswait

import (
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

func validateFenceIdentity(f FenceSnapshot) error {
	if err := validateText(ErrInvalidAccess, "agent_id", f.AgentID, maxID, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidAccess, "reservation_id", f.ReservationID, maxID, false); err != nil {
		return err
	}
	if f.ReservationGeneration <= 0 {
		return invalidAccess("reservation_generation must be positive")
	}
	return validateText(ErrInvalidAccess, "source_fence_ref", f.SourceFenceRef, maxRef, false)
}

func validateUnboundFence(f FenceSnapshot) error {
	if f.IncarnationGeneration != 0 || f.BindingVersion != 0 || f.BindingDigest != "" {
		return invalidAccess("unbound fence contains incarnation fields")
	}
	return nil
}

func validateBoundFenceFields(f FenceSnapshot) error {
	if err := validateText(ErrInvalidAccess, "incarnation_id", f.IncarnationID, maxID, false); err != nil {
		return err
	}
	if f.IncarnationGeneration <= 0 || f.BindingVersion <= 0 {
		return invalidAccess("bound fence generations must be positive")
	}
	return validateDigest(ErrInvalidAccess, "binding_digest", f.BindingDigest, true)
}

func validateGrantRequestIdentity(r AccessGrantRequest) error {
	if err := validateText(ErrInvalidAccess, "grant_id", r.GrantID, maxID, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidAccess, "caller_principal_ref", r.CallerPrincipalRef, maxRef, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidAccess, "scope_ref", r.ScopeRef, maxRef, false); err != nil {
		return err
	}
	return nil
}

func validateDistinctBoundFences(source, target FenceSnapshot) error {
	if err := validateCanonicalSourceFence(source); err != nil {
		return err
	}
	if err := source.ValidateBound(); err != nil {
		return err
	}
	if err := target.ValidateBound(); err != nil {
		return err
	}
	if source.AgentID == target.AgentID {
		return invalidAccess("source and target agents must differ")
	}
	return nil
}

// validateCanonicalSourceFence 固定 source fence 到 task event source 的
// canonical identity。AccessGrant 没有可替代 source_id/source_epoch 的字段，
// 因此这条派生关系必须在 grant/request 恢复与匹配时重复验证。
func validateCanonicalSourceFence(source FenceSnapshot) error {
	if err := source.ValidateBound(); err != nil {
		return err
	}
	if strings.ContainsAny(source.AgentID, "/\\") {
		return invalidAccess("source fence contains unsafe canonical identity component")
	}
	return nil
}

func validateGrantRequestProofs(r AccessGrantRequest) error {
	if err := validateText(ErrInvalidAccess, "idempotency_namespace", r.IdempotencyNamespace, maxNamespace, false); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidAccess, "idempotency_key_digest", r.IdempotencyKeyDigest, true); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidAccess, "request_digest", r.RequestDigest, true); err != nil {
		return err
	}
	return validateTimeWindow(ErrInvalidAccess, r.IssuedAt, r.ExpiresAt)
}

func accessGrantRequestFromGrant(g AccessGrant) AccessGrantRequest {
	return AccessGrantRequest{
		GrantID: g.GrantID, CallerPrincipalRef: g.CallerPrincipalRef, ScopeRef: g.ScopeRef,
		Source: g.Source, Target: g.Target, CanRead: g.CanRead, CanSend: g.CanSend,
		IdempotencyNamespace: g.IdempotencyNamespace, IdempotencyKeyDigest: g.IdempotencyKeyDigest,
		RequestDigest: g.RequestDigest, IssuedAt: g.IssuedAt, ExpiresAt: g.ExpiresAt,
	}
}

// validatePersistedGrant 校验从数据库恢复的授权状态、版本和终态时间戳。
func validatePersistedGrant(g AccessGrant) error {
	if g.Revision <= 0 || g.SchemaVersion != contractVersion {
		return invalidAccess("grant revision or schema version is invalid")
	}
	if !validGrantStatus(g.Status) {
		return invalidAccess("grant status is unsupported")
	}
	if g.CreatedAt.IsZero() || g.UpdatedAt.IsZero() || g.UpdatedAt.Before(g.CreatedAt) {
		return invalidAccess("grant timestamps are invalid")
	}
	return validateGrantTerminalTime(g)
}

func validGrantStatus(status string) bool {
	return status == "active" || status == "revoked" || status == "expired"
}

// validateGrantTerminalTime 确保 active 与 revoked/expired 的时间戳严格 one-hot。
func validateGrantTerminalTime(g AccessGrant) error {
	if err := validateGrantTerminalStatus(g); err != nil {
		return err
	}
	return validateGrantTerminalOneHot(g)
}

func validateGrantTerminalStatus(g AccessGrant) error {
	if g.Status == "revoked" && !validTerminalTime(g.RevokedAt, g.CreatedAt) {
		return invalidAccess("revoked grant requires revoked_at")
	}
	if g.Status == "expired" && !validTerminalTime(g.ExpiredAt, g.CreatedAt) {
		return invalidAccess("expired grant requires expired_at")
	}
	return nil
}

func validTerminalTime(value *time.Time, created time.Time) bool {
	return value != nil && !value.IsZero() && !value.Before(created)
}

// validateGrantTerminalOneHot 拒绝 active 与任一终态时间戳混合出现。
func validateGrantTerminalOneHot(g AccessGrant) error {
	if g.Status == "active" && (g.RevokedAt != nil || g.ExpiredAt != nil) {
		return invalidAccess("active grant cannot carry terminal timestamp")
	}
	if g.Status == "revoked" && g.ExpiredAt != nil || g.Status == "expired" && g.RevokedAt != nil {
		return invalidAccess("grant terminal timestamps are not one-hot")
	}
	return nil
}

// validateRevokeGrantRequest 校验撤销请求的 caller、摘要和 revision 边界。
func validateRevokeGrantRequest(r RevokeGrantRequest) error {
	if err := validateText(ErrInvalidAccess, "grant_id", r.GrantID, maxID, false); err != nil {
		return err
	}
	if r.GrantRevision <= 0 {
		return invalidAccess("grant_revision must be positive")
	}
	if err := validateText(ErrInvalidAccess, "caller_principal_ref", r.CallerPrincipalRef, maxRef, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidAccess, "scope_ref", r.ScopeRef, maxRef, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidAccess, "reason_ref", r.ReasonRef, maxRef, false); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidAccess, "request_digest", r.RequestDigest, true); err != nil {
		return err
	}
	if r.RevokedAt.IsZero() || r.RevokedAt.Location() != time.UTC {
		return invalidAccess("revoked_at is required")
	}
	return nil
}

// Validate 校验安全时间 owner 的到期 CAS 输入。
func (r ExpireGrantRequest) Validate() error {
	if err := validateText(ErrInvalidAccess, "grant_id", r.GrantID, maxID, false); err != nil {
		return err
	}
	if r.ExpectedRevision <= 0 {
		return invalidAccess("expected_revision must be positive")
	}
	if r.ExpiredAt.IsZero() || r.ExpiredAt.Location() != time.UTC {
		return invalidAccess("expired_at must be UTC")
	}
	return nil
}

func validateAccessRequestIdentity(r AccessRequest) error {
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{"request_id", r.RequestID, maxID}, {"grant_id", r.GrantID, maxID},
		{"caller_principal_ref", r.CallerPrincipalRef, maxRef}, {"scope_ref", r.ScopeRef, maxRef},
	} {
		if err := validateText(ErrInvalidAccess, field.name, field.value, field.limit, false); err != nil {
			return err
		}
	}
	return nil
}

func validateAccessRequestProof(r AccessRequest) error {
	if err := validateDigest(ErrInvalidAccess, "request_digest", r.RequestDigest, true); err != nil {
		return err
	}
	if r.RequestedAt.IsZero() || r.RequestedAt.Location() != time.UTC {
		return invalidAccess("requested_at is required")
	}
	return nil
}

func validateAuthorizationIdentity(a AccessAuthorization) error {
	if err := validateText(ErrInvalidAccess, "request_id", a.RequestID, maxID, false); err != nil {
		return err
	}
	return validateText(ErrInvalidAccess, "grant_id", a.GrantID, maxID, false)
}

// validateAuthorizationWindow 校验判决时间窗和 grant revision 的一致性。
func validateAuthorizationWindow(a AccessAuthorization) error {
	if a.GrantRevision <= 0 || a.AuthorizedAt.IsZero() || a.ExpiresAt.IsZero() || a.AuthorizedAt.Location() != time.UTC || a.ExpiresAt.Location() != time.UTC || !a.ExpiresAt.After(a.AuthorizedAt) {
		return invalidAccess("authorization revision or time window is invalid")
	}
	return nil
}

// validateAuthorizationGrant 确认授权结果仍指向同一 grant revision 和 capability。
func validateAuthorizationGrant(a AccessAuthorization) error {
	if a.Grant.Status != "active" {
		return invalidAccess("authorization cannot contain a terminal grant")
	}
	if a.Grant.GrantID != a.GrantID || a.Grant.Revision != a.GrantRevision {
		return invalidAccess("authorization grant does not match")
	}
	if a.Capability == "read" && !a.Grant.CanRead || a.Capability == "send" && !a.Grant.CanSend {
		return invalidAccess("grant does not contain capability")
	}
	return nil
}

func validateSelectorHeader(s EventSelector) error {
	if s.Version != contractVersion {
		return invalidWait("selector version must be 1")
	}
	if err := validateText(ErrInvalidWait, "source_id", s.SourceID, maxSourceID, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidWait, "source_epoch", s.SourceEpoch, maxID, false); err != nil {
		return err
	}
	if !validSelectorLane(s.Lane) {
		return invalidWait("selector lane is unsupported")
	}
	return validateText(ErrInvalidWait, "target_agent_id", s.TargetAgentID, maxID, false)
}

func validSelectorLane(lane eventstore.Lane) bool {
	return lane == eventstore.LaneControl || lane == eventstore.LaneOutput || lane == eventstore.LaneContent
}

// validateSelectorEventTypes 校验事件类型集合有界、唯一且按规范顺序排列。
func validateSelectorEventTypes(eventTypes []string) error {
	if len(eventTypes) == 0 || len(eventTypes) > maxSelectorEvents {
		return invalidWait("selector event_types count is invalid")
	}
	seen := make(map[string]struct{}, len(eventTypes))
	for index, eventType := range eventTypes {
		if err := validateText(ErrInvalidWait, "event_type", eventType, maxEventType, false); err != nil {
			return err
		}
		if _, exists := seen[eventType]; exists || (index > 0 && eventTypes[index-1] >= eventType) {
			return invalidWait("selector event_types must be unique and sorted")
		}
		seen[eventType] = struct{}{}
	}
	return nil
}

// validateWaitRequestIdentity 校验 wait 句柄、caller 和 scope 的身份字段。
func validateWaitRequestIdentity(r WaitRequest) error {
	if r.WaitHandleID != "" {
		if err := validateText(ErrInvalidWait, "wait_handle_id", r.WaitHandleID, maxID, false); err != nil {
			return err
		}
	}
	if err := validateText(ErrInvalidWait, "grant_id", r.GrantID, maxID, false); err != nil {
		return err
	}
	if r.GrantRevision <= 0 {
		return invalidWait("grant_revision must be positive")
	}
	if err := validateText(ErrInvalidWait, "caller_principal_ref", r.CallerPrincipalRef, maxRef, false); err != nil {
		return err
	}
	return validateText(ErrInvalidWait, "scope_ref", r.ScopeRef, maxRef, false)
}

// validateWaitCursor 校验 dense source tuple 对应的 after cursor，不接受任意字符串。
func validateWaitCursor(r WaitRequest) error {
	if r.AfterSequence < 0 {
		return invalidWait("after_sequence cannot be negative")
	}
	if r.AfterSequence == 0 {
		if r.AfterCursor != "" {
			return invalidWait("initial wait cursor must be empty")
		}
		return nil
	}
	if r.AfterCursor == "" {
		return invalidWait("after_cursor is required after the initial sequence")
	}
	if err := validateText(ErrInvalidWait, "after_cursor", r.AfterCursor, maxRef, false); err != nil {
		return err
	}
	want := fmt.Sprintf("%s/%s/%s/%d", r.Selector.SourceID, r.Selector.SourceEpoch, r.Selector.Lane, r.AfterSequence)
	if r.AfterCursor != want {
		return invalidWait("after_cursor does not match selector source tuple")
	}
	return nil
}

func validateWaitRequestProof(r WaitRequest) error {
	if r.DeadlineAt.IsZero() || r.DeadlineAt.Location() != time.UTC {
		return invalidWait("deadline_at is required")
	}
	if err := validateText(ErrInvalidWait, "idempotency_namespace", r.IdempotencyNamespace, maxNamespace, false); err != nil {
		return err
	}
	if err := validateDigest(ErrInvalidWait, "idempotency_key_digest", r.IdempotencyKeyDigest, true); err != nil {
		return err
	}
	return validateDigest(ErrInvalidWait, "request_digest", r.RequestDigest, true)
}

func waitRequestFromHandle(h WaitHandle) WaitRequest {
	return WaitRequest{WaitHandleID: h.WaitHandleID, GrantID: h.GrantID, GrantRevision: h.GrantRevision, CallerPrincipalRef: h.CallerPrincipalRef, ScopeRef: h.ScopeRef, Selector: h.Selector, AfterSequence: h.AfterSequence, AfterCursor: h.AfterCursor, DeadlineAt: h.DeadlineAt, IdempotencyNamespace: h.IdempotencyNamespace, IdempotencyKeyDigest: h.IdempotencyKeyDigest, RequestDigest: h.RequestDigest}
}

func validateWaitHandleBinding(h WaitHandle) error {
	if err := validateText(ErrInvalidWait, "wait source agent", h.SourceAgentID, maxID, false); err != nil {
		return err
	}
	if err := h.Target.ValidateBound(); err != nil {
		return err
	}
	if h.SourceAgentID == h.Target.AgentID || h.Target.AgentID != h.Selector.TargetAgentID {
		return invalidWait("wait target fence does not match selector or source")
	}
	return nil
}

// validateWaitHandleMetadata 校验 durable wait 的 revision、时间和状态字段。
func validateWaitHandleMetadata(h WaitHandle) error {
	if h.Revision <= 0 || h.CreatedAt.IsZero() || h.UpdatedAt.IsZero() || h.UpdatedAt.Before(h.CreatedAt) {
		return invalidWait("wait handle revision or timestamps are invalid")
	}
	if !validWaitStatus(h.Status) {
		return invalidWait("wait handle status is unsupported")
	}
	return nil
}

// validateWaitMatch 校验匹配事件引用的 one-hot 结构和有界摘要。
func validateWaitMatch(h WaitHandle) error {
	if h.MatchedEventID == "" {
		if h.MatchedSourceSequence != 0 || h.MatchedCanonicalCursor != "" {
			return invalidWait("unmatched wait contains match fields")
		}
		return nil
	}
	if err := validateText(ErrInvalidWait, "matched_event_id", h.MatchedEventID, maxID, false); err != nil {
		return err
	}
	if h.MatchedSourceSequence <= 0 || h.MatchedCanonicalCursor == "" {
		return invalidWait("matched event requires sequence and cursor")
	}
	return validateText(ErrInvalidWait, "matched_canonical_cursor", h.MatchedCanonicalCursor, maxRef, false)
}

func validateWaitResolution(h WaitHandle) error {
	if h.Status == "pending" {
		if h.ResolvedAt != nil {
			return invalidWait("pending wait cannot carry resolved_at")
		}
		return nil
	}
	if h.ResolvedAt == nil || h.ResolvedAt.IsZero() {
		return invalidWait("terminal wait requires resolved_at")
	}
	return nil
}

func validateWaitResultState(r WaitResult) error {
	if !validWaitResultStatus(r.Status) {
		return invalidWait("wait result status is unsupported")
	}
	if r.Status != r.Handle.Status && !(r.Handle.Status == "pending" && r.Status == "indeterminate") {
		return invalidWait("wait result status does not match handle")
	}
	return nil
}

func validateWaitResultCursor(cursor string) error {
	if cursor == "" {
		return nil
	}
	return validateText(ErrInvalidWait, "next_cursor", cursor, maxRef, false)
}

func validateWaitResultEvent(r WaitResult) error {
	if r.Event == nil {
		return nil
	}
	if err := r.Event.Validate(); err != nil {
		return err
	}
	if r.Event.Envelope.SubjectAgentID != r.SelectorTargetAgentID() {
		return invalidWait("result event is outside selector target")
	}
	return nil
}

func validateWaitResultEventStatus(r WaitResult) error {
	if r.Status == "satisfied" && r.Event == nil {
		return invalidWait("satisfied wait result lacks event")
	}
	if r.Status != "satisfied" && r.Event != nil {
		return invalidWait("non-satisfied wait result carries event")
	}
	return nil
}
