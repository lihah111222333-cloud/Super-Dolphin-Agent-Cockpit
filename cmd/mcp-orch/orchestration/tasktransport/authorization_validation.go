package tasktransport

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// ValidateAt 在指定安全时间验证 caller 的完整 task 身份快照。
func (c CallerBinding) ValidateAt(now time.Time) error {
	if !validUTC(now) {
		return invalidAuthorization("caller validation time is invalid")
	}
	if err := validateCallerFields(c); err != nil {
		return err
	}
	if err := validateCallerLifetime(c, now); err != nil {
		return err
	}
	return validateCallerAttribution(c)
}

// validateCallerFields 验证 caller 的文本字段和稳定 session/boot 标识。
func validateCallerFields(c CallerBinding) error {
	fields := []struct {
		value string
		limit int
	}{
		{c.PrincipalRef, 512}, {c.AuthContextID, 512}, {c.ScopeRef, 512},
		{c.Profile, 128}, {c.TaskID, 128}, {c.SessionID, 128}, {c.BootEpoch, 128},
	}
	for _, field := range fields {
		if !validText(field.value, field.limit, true) {
			return invalidAuthorization("caller fields are not canonical")
		}
	}
	return nil
}

// validateCallerLifetime 防止 operator、过期或未绑定 task 的 caller 进入授权。
func validateCallerLifetime(c CallerBinding, now time.Time) error {
	if strings.EqualFold(c.Profile, "operator") || !validUTC(c.ExpiresAt) || !c.ExpiresAt.After(now) || c.TaskID != c.Source.AgentID || c.AuthContextID != c.SessionID || c.BootEpoch != c.Issuer.BindingRef {
		return invalidAuthorization("caller is not an active task principal")
	}
	return nil
}

// validateCallerAttribution 复用 common 私有归因结构的完整校验。
func validateCallerAttribution(c CallerBinding) error {
	attribution := common.CallerAttribution{SchemaVersion: common.CallerAttributionSchemaVersion, SubjectKind: common.CallerAttributionSubjectKindTask, TaskID: c.TaskID, Source: c.Source, Issuer: c.Issuer, Audience: common.CallerAttributionAudience}
	if attribution.Validate() != nil {
		return invalidAuthorization("caller attribution is invalid")
	}
	return nil
}

// Validate 检查 parent/child 关系和两端完整 execution fence。
func (p ParentBinding) Validate() error {
	if p.SchemaVersion != AuthorizationSchemaVersion || p.Relation != ParentChildRelation && p.Relation != PeerRelation {
		return invalidAuthorization("parent relation is unsupported")
	}
	if err := validateParentIDs(p); err != nil {
		return err
	}
	if !validText(p.ParentScopeRef, 512, true) || !validText(p.ChildScopeRef, 512, true) || !validText(p.ParentEvidenceRef, 512, true) || !validDigest(p.BindingDigest) {
		return invalidAuthorization("parent binding evidence is incomplete")
	}
	if err := validateFence(p.ParentFence); err != nil {
		return err
	}
	return validateFence(p.ChildFence)
}

// validateParentIDs 验证关系两端 task id 与 fence agent id 逐字相等。
func validateParentIDs(p ParentBinding) error {
	if !validText(p.ParentTaskID, 128, true) || !validText(p.ChildTaskID, 128, true) || p.ParentTaskID == p.ChildTaskID || p.ParentTaskID != p.ParentFence.AgentID || p.ChildTaskID != p.ChildFence.AgentID {
		return invalidAuthorization("parent task binding is invalid")
	}
	return nil
}

// Validate 检查 grant reference 的稳定标识和两个重放围栏。
func (r GrantRef) Validate() error {
	if r.SchemaVersion != AuthorizationSchemaVersion || !validText(r.GrantID, 128, true) || r.GrantRevision <= 0 || r.RevocationEpoch <= 0 || !validDigest(r.GrantDigest) {
		return invalidAuthorization("grant reference is invalid")
	}
	return nil
}

// validateShape 检查 grant 的不可变字段，不读取任何 owner 或数据库。
func (g GrantClaim) validateShape() error {
	if g.SchemaVersion != AuthorizationSchemaVersion {
		return invalidAuthorization("grant schema version is unsupported")
	}
	if err := validateGrantIdentityFields(g); err != nil {
		return err
	}
	if err := g.Parent.Validate(); err != nil {
		return err
	}
	if err := validateGrantScopes(g); err != nil {
		return err
	}
	if err := validateGrantEvidence(g); err != nil {
		return err
	}
	return validateGrantTerminal(g)
}

// validateGrantIdentityFields 验证 grant id、action、caller 和 scope。
func validateGrantIdentityFields(g GrantClaim) error {
	if !validText(g.GrantID, 128, true) || !validAction(g.Action) || !validText(g.CallerPrincipalRef, 512, true) || !validText(g.ScopeRef, 512, true) || !validText(g.SourceScopeRef, 512, true) || !validText(g.TargetScopeRef, 512, true) {
		return invalidAuthorization("grant identity is invalid")
	}
	return nil
}

// validateGrantScopes 确认 grant scope 与 parent binding scope 逐字一致。
func validateGrantScopes(g GrantClaim) error {
	if g.SourceScopeRef != g.Parent.ParentScopeRef || g.TargetScopeRef != g.Parent.ChildScopeRef {
		return ErrParentMismatch
	}
	return nil
}

// validateGrantEvidence 验证 grant revision、evidence cursor、digest 和时间窗。
func validateGrantEvidence(g GrantClaim) error {
	if g.GrantRevision <= 0 || g.RevocationEpoch <= 0 || !validText(g.CapabilityEvidenceCursor, 512, true) || !validDigest(g.RequestDigest) || !validDigest(g.GrantDigest) {
		return invalidAuthorization("grant evidence is incomplete")
	}
	if !validUTC(g.IssuedAt) || !validUTC(g.ExpiresAt) || !g.ExpiresAt.After(g.IssuedAt) {
		return invalidAuthorization("grant time window is invalid")
	}
	return nil
}

// ValidateAt 在指定安全时间检查 grant 的有效时间窗。
func (g GrantClaim) ValidateAt(now time.Time) error {
	if err := g.validateShape(); err != nil {
		return err
	}
	if !validUTC(now) {
		return invalidAuthorization("authorization time is invalid")
	}
	if g.Status == "active" && now.Before(g.IssuedAt) {
		return ErrGrantNotYetValid
	}
	if g.Status == "active" && !now.Before(g.ExpiresAt) {
		return ErrGrantExpired
	}
	return nil
}

// validateGrantTerminal 检查 active/revoked/expired 的终态字段 one-hot。
func validateGrantTerminal(g GrantClaim) error {
	switch g.Status {
	case "active":
		if g.RevokedAt != nil || g.ExpiredAt != nil {
			return invalidAuthorization("active grant carries terminal timestamp")
		}
	case "revoked":
		if !validTerminalTime(g.RevokedAt, g.IssuedAt) || g.ExpiredAt != nil {
			return invalidAuthorization("revoked grant terminal state is invalid")
		}
	case "expired":
		if !validTerminalTime(g.ExpiredAt, g.IssuedAt) || g.RevokedAt != nil {
			return invalidAuthorization("expired grant terminal state is invalid")
		}
	default:
		return invalidAuthorization("grant status is unsupported")
	}
	return nil
}

// Validate 检查 cursor 的 source owner、session、selector 和 exclusive position。
func (c CursorClaim) Validate() error {
	if c.SchemaVersion != AuthorizationSchemaVersion {
		return invalidAuthorization("cursor schema version is unsupported")
	}
	if err := validateCursorIdentity(c); err != nil {
		return err
	}
	if err := c.Grant.Validate(); err != nil {
		return err
	}
	if err := validateFence(c.SourceFence); err != nil {
		return err
	}
	if err := validateFence(c.TargetFence); err != nil {
		return err
	}
	if err := validateCursorSource(c); err != nil {
		return err
	}
	return validateCursorPosition(c)
}

// validateCursorIdentity 验证 cursor 的 domain、owner、session 和 selector 字段。
func validateCursorIdentity(c CursorClaim) error {
	if err := validateCursorTexts(c); err != nil {
		return err
	}
	if c.SourceOwnerGeneration <= 0 || !validLane(c.Lane) {
		return invalidAuthorization("cursor owner or lane is invalid")
	}
	return nil
}

// validateCursorTexts 验证 cursor 的 bounded text 和 selector digest。
func validateCursorTexts(c CursorClaim) error {
	if !validText(c.Domain, 128, true) || !validText(c.SourceOwner, 256, true) || !validText(c.SourceID, 256, true) || !validText(c.SourceEpoch, 128, true) || !validText(c.StreamID, 128, true) || !validText(c.SessionID, 128, true) || !validText(c.BootEpoch, 128, true) || !validDigest(c.SelectorDigest) {
		return invalidAuthorization("cursor identity is invalid")
	}
	return nil
}

// validateCursorSource 将 source id/epoch 绑定到其 execution fence。
func validateCursorSource(c CursorClaim) error {
	if c.SourceID != "task/"+c.SourceFence.AgentID || c.SourceEpoch != c.SourceFence.IncarnationID {
		return ErrCursorMismatch
	}
	return nil
}

// validateCursorPosition 检查 exclusive sequence 与 canonical cursor 的关系。
func validateCursorPosition(c CursorClaim) error {
	if c.AfterSequence < 0 {
		return invalidAuthorization("cursor sequence is negative")
	}
	if c.AfterSequence == 0 && c.AfterCursor != "" {
		return ErrCursorMismatch
	}
	if c.AfterSequence > 0 {
		if !validText(c.AfterCursor, 512, true) || c.AfterCursor != canonicalCursor(c.SourceID, c.SourceEpoch, c.Lane, c.AfterSequence) {
			return ErrCursorMismatch
		}
	}
	return nil
}

// ValidateAt 检查 authorization request 的 caller、grant、parent 和 action 分支。
func (r AuthorizationRequest) ValidateAt(now time.Time) error {
	if err := validateRequestHeader(r); err != nil {
		return err
	}
	if err := r.Caller.ValidateAt(now); err != nil {
		return err
	}
	if err := r.Grant.ValidateAt(now); err != nil {
		return err
	}
	if err := validateRequestGrantParent(r); err != nil {
		return err
	}
	if err := validateRequestCaller(r); err != nil {
		return err
	}
	if r.Action != r.Grant.Action {
		return ErrGrantActionMismatch
	}
	if r.RequestedAt.After(now) || r.RequestedAt.Before(r.Grant.IssuedAt) || !r.RequestedAt.Before(r.Caller.ExpiresAt) {
		return ErrUnauthorized
	}
	return r.validateActionFields()
}

// validateRequestHeader 验证 request id、时间和 intent digest。
func validateRequestHeader(r AuthorizationRequest) error {
	if r.SchemaVersion != AuthorizationSchemaVersion || !validText(r.RequestID, 128, true) || !validUTC(r.RequestedAt) || !validDigest(r.IntentDigest) {
		return invalidAuthorization("request identity is invalid")
	}
	return nil
}

// validateRequestGrantParent 验证 grant ref 和 parent binding 的完整重放相等性。
func validateRequestGrantParent(r AuthorizationRequest) error {
	if err := r.GrantRef.Validate(); err != nil || !sameGrantRef(r.GrantRef, r.Grant.Ref()) {
		return ErrGrantActionMismatch
	}
	if !sameParent(r.Parent, r.Grant.Parent) {
		return ErrParentMismatch
	}
	return nil
}

// validateRequestCaller 确认认证 caller 与 grant parent 的 source 完整相等。
func validateRequestCaller(r AuthorizationRequest) error {
	if r.Caller.PrincipalRef != r.Grant.CallerPrincipalRef || r.Caller.ScopeRef != r.Grant.ScopeRef || r.Caller.TaskID != r.Parent.ParentTaskID || !sameFence(r.Caller.Source, r.Parent.ParentFence) {
		return ErrUnauthorized
	}
	return nil
}

// validateActionFields 执行 discover/read/send 的互斥字段校验。
func (r AuthorizationRequest) validateActionFields() error {
	switch r.Action {
	case ActionDiscover:
		return validateDiscoverFields(r)
	case ActionRead:
		return validateReadFields(r)
	case ActionSend:
		return validateSendFields(r)
	default:
		return invalidAuthorization("request action is unsupported")
	}
}

// validateDiscoverFields 确保 discover 不携带 read/send 的资源或 cursor。
func validateDiscoverFields(r AuthorizationRequest) error {
	if r.Cursor != nil || r.ContentRef != "" || r.ResourceReservationRef != "" {
		return ErrGrantActionMismatch
	}
	return nil
}

// validateReadFields 确保 read 使用同一 caller session 的独立 cursor。
func validateReadFields(r AuthorizationRequest) error {
	if r.Cursor == nil || r.ContentRef != "" || r.ResourceReservationRef != "" {
		return ErrCursorMismatch
	}
	if err := r.Cursor.Validate(); err != nil {
		return err
	}
	if !sameGrantRef(r.Cursor.Grant, r.Grant.Ref()) || !sameFence(r.Cursor.SourceFence, r.Parent.ParentFence) || !sameFence(r.Cursor.TargetFence, r.Parent.ChildFence) || r.Cursor.SessionID != r.Caller.SessionID || r.Cursor.BootEpoch != r.Caller.BootEpoch {
		return ErrCursorMismatch
	}
	return nil
}

// validateSendFields 确保 send 只携带 content/resource opaque refs，不带 read cursor。
func validateSendFields(r AuthorizationRequest) error {
	if r.Cursor != nil || !validText(r.ContentRef, 512, true) || !validText(r.ResourceReservationRef, 512, true) {
		return ErrGrantActionMismatch
	}
	return nil
}

// Validate 检查撤销请求的 grant ref、理由、操作摘要和安全时间。
func (r RevokeGrantRequest) Validate() error {
	if r.SchemaVersion != AuthorizationSchemaVersion {
		return invalidAuthorization("revocation schema version is unsupported")
	}
	if err := r.GrantRef.Validate(); err != nil || !validText(r.ReasonRef, 512, true) || !validDigest(r.OperationDigest) || !validUTC(r.RequestedAt) {
		return invalidAuthorization("revocation request is invalid")
	}
	if err := r.Caller.ValidateAt(r.RequestedAt); err != nil {
		return err
	}
	if err := r.Parent.Validate(); err != nil {
		return err
	}
	return nil
}

// ValidateAt 检查撤销请求在实际评估时刻仍由有效 caller 发起。
// ValidateAt keeps the structural request validation separate from the
// time-of-use check so an old RequestedAt cannot bypass caller expiry.
func (r RevokeGrantRequest) ValidateAt(now time.Time) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if !validUTC(now) {
		return invalidAuthorization("revocation evaluation time is invalid")
	}
	if r.RequestedAt.After(now) {
		return ErrRevocationConflict
	}
	return r.Caller.ValidateAt(now)
}

// validateFence 检查 execution fence 的每个代际和摘要字段。
func validateFence(f common.ExecutionFence) error {
	if !validText(f.AgentID, 128, true) || !validText(f.ReservationID, 128, true) || !validText(f.IncarnationID, 128, true) || !validText(f.SourceFenceRef, 512, true) || f.ReservationGeneration <= 0 || f.IncarnationGeneration <= 0 || f.BindingVersion <= 0 || !validDigest(f.BindingDigest) {
		return invalidAuthorization("execution fence is invalid")
	}
	return nil
}

// sameCaller 比较 context 解析结果与请求 caller 的完整字段。
func sameCaller(left, right CallerBinding) bool {
	return left.PrincipalRef == right.PrincipalRef && left.AuthContextID == right.AuthContextID && left.ScopeRef == right.ScopeRef && left.Profile == right.Profile && left.TaskID == right.TaskID && left.SessionID == right.SessionID && left.BootEpoch == right.BootEpoch && sameFence(left.Source, right.Source) && left.Issuer == right.Issuer && left.ExpiresAt.Equal(right.ExpiresAt)
}

// sameParent 比较 parent/child relation 与全部 fence/evidence 字段。
func sameParent(left, right ParentBinding) bool {
	return left.Relation == right.Relation && left.ParentTaskID == right.ParentTaskID && left.ChildTaskID == right.ChildTaskID && left.ParentScopeRef == right.ParentScopeRef && left.ChildScopeRef == right.ChildScopeRef && sameFence(left.ParentFence, right.ParentFence) && sameFence(left.ChildFence, right.ChildFence) && left.ParentEvidenceRef == right.ParentEvidenceRef && left.BindingDigest == right.BindingDigest
}

// sameGrantRef 比较 grant id、revision、revocation epoch 和 digest。
func sameGrantRef(left, right GrantRef) bool {
	return left.GrantID == right.GrantID && left.GrantRevision == right.GrantRevision && left.RevocationEpoch == right.RevocationEpoch && left.GrantDigest == right.GrantDigest
}

// sameFence 比较 execution fence 的所有结构字段。
func sameFence(left, right common.ExecutionFence) bool {
	return left == right
}

// validAction 判断 action 是否属于闭合集合。
func validAction(action Action) bool {
	return action == ActionDiscover || action == ActionRead || action == ActionSend
}

// validLane 判断 cursor lane 是否属于 canonical 三 lane。
func validLane(lane string) bool {
	return lane == "control" || lane == "output" || lane == "content"
}

// canonicalCursor 生成 source tuple 对应的 exclusive cursor 文本。
func canonicalCursor(sourceID, sourceEpoch, lane string, sequence int64) string {
	return fmt.Sprintf("%s/%s/%s/%d", sourceID, sourceEpoch, lane, sequence)
}

// validTerminalTime 检查终态时间为 UTC 且不早于签发时间。
func validTerminalTime(value *time.Time, issued time.Time) bool {
	return value != nil && validUTC(*value) && !value.Before(issued)
}

// validUTC 判断时间是否为非零 UTC 值。
func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

// validText 判断文本是否满足长度、UTF-8、空白和控制字符约束。
func validText(value string, limit int, required bool) bool {
	if value == "" {
		return !required
	}
	if len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value || strings.ContainsRune(value, 0) {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// validDigest 判断摘要是否为小写 256-bit 十六进制。
func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// invalidAuthorization 将结构错误归入稳定 invalid sentinel。
func invalidAuthorization(reason string) error {
	return fmt.Errorf("%w: %s", ErrAuthorizationInvalid, reason)
}
