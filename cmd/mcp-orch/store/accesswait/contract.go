// Package accesswait 定义编排访问授权与可恢复 wait 的存储无关合同。
//
// 本包只负责不变量和窄端口；它不创建授权、不读取 SQLite，也不把请求
// 参数解释为身份或权限。真正的 owner 必须在持久化线性化点再次验证这些值。
package accesswait

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// 这些错误是 wire/store 层的稳定分类；调用方应使用 errors.Is，而不是匹配文本。
var (
	ErrInvalidAccess     = errors.New("access input is invalid")
	ErrAccessDenied      = errors.New("access is denied")
	ErrAccessConflict    = errors.New("access operation conflicts with existing grant")
	ErrAccessNotFound    = errors.New("access grant was not found")
	ErrAccessExpired     = errors.New("access grant has expired")
	ErrAccessRevoked     = errors.New("access grant has been revoked")
	ErrInvalidWait       = errors.New("wait input is invalid")
	ErrWaitNotFound      = errors.New("wait handle was not found")
	ErrWaitGap           = errors.New("wait event cursor has a gap")
	ErrWaitQuarantined   = errors.New("wait source is quarantined")
	ErrWaitCursorAhead   = errors.New("wait cursor is ahead of the canonical source")
	ErrWaitReplayExpired = errors.New("wait cursor is below the canonical replay floor")
	ErrWaitScanLimit     = errors.New("wait event scan exceeded bounded limit")
	ErrWaitTerminal      = errors.New("wait handle is terminal")
)

const contractVersion int64 = 1

const (
	maxID             = 128
	maxRef            = 512
	maxSourceID       = 256
	maxNamespace      = 256
	maxEventType      = 128
	maxSelectorEvents = 32
)

// FenceSnapshot 是授权所绑定的身份代际快照；空的 incarnation 表示尚未激活。
// 任何字段都不能由 agent_id、目录或展示名推导，owner 必须逐字段比较当前事实。
type FenceSnapshot struct {
	AgentID               string `json:"agent_id"`
	ReservationID         string `json:"reservation_id"`
	ReservationGeneration int64  `json:"reservation_generation"`
	IncarnationID         string `json:"incarnation_id,omitempty"`
	IncarnationGeneration int64  `json:"incarnation_generation"`
	BindingVersion        int64  `json:"binding_version"`
	BindingDigest         string `json:"binding_digest,omitempty"`
	SourceFenceRef        string `json:"source_fence_ref"`
}

// AccessGrantRequest 是显式、带方向的读写授权申请；申请本身不代表已授权。
type AccessGrantRequest struct {
	GrantID              string        `json:"grant_id"`
	CallerPrincipalRef   string        `json:"caller_principal_ref"`
	ScopeRef             string        `json:"scope_ref"`
	Source               FenceSnapshot `json:"source"`
	Target               FenceSnapshot `json:"target"`
	CanRead              bool          `json:"can_read"`
	CanSend              bool          `json:"can_send"`
	IdempotencyNamespace string        `json:"idempotency_namespace"`
	IdempotencyKeyDigest string        `json:"idempotency_key_digest"`
	RequestDigest        string        `json:"request_digest"`
	IssuedAt             time.Time     `json:"issued_at"`
	ExpiresAt            time.Time     `json:"expires_at"`
}

// AccessGrant 是可审计的授权快照；状态只能由授权 owner 推进为 active、revoked 或 expired。
type AccessGrant struct {
	GrantID              string        `json:"grant_id"`
	CallerPrincipalRef   string        `json:"caller_principal_ref"`
	ScopeRef             string        `json:"scope_ref"`
	Source               FenceSnapshot `json:"source"`
	Target               FenceSnapshot `json:"target"`
	CanRead              bool          `json:"can_read"`
	CanSend              bool          `json:"can_send"`
	IdempotencyNamespace string        `json:"idempotency_namespace"`
	IdempotencyKeyDigest string        `json:"idempotency_key_digest"`
	RequestDigest        string        `json:"request_digest"`
	IssuedAt             time.Time     `json:"issued_at"`
	ExpiresAt            time.Time     `json:"expires_at"`
	Revision             int64         `json:"revision"`
	Status               string        `json:"status"`
	SchemaVersion        int64         `json:"schema_version"`
	CreatedAt            time.Time     `json:"created_at"`
	UpdatedAt            time.Time     `json:"updated_at"`
	RevokedAt            *time.Time    `json:"revoked_at,omitempty"`
	ExpiredAt            *time.Time    `json:"expired_at,omitempty"`
}

// RevokeGrantRequest 显式撤销当前 revision；旧 revision 不能越过 CAS 复活授权。
type RevokeGrantRequest struct {
	GrantID            string    `json:"grant_id"`
	GrantRevision      int64     `json:"grant_revision"`
	CallerPrincipalRef string    `json:"caller_principal_ref"`
	ScopeRef           string    `json:"scope_ref"`
	ReasonRef          string    `json:"reason_ref"`
	RequestDigest      string    `json:"request_digest"`
	RevokedAt          time.Time `json:"revoked_at"`
}

// ExpireGrantRequest 是安全时间 owner 用于推进已到期 grant 的 CAS 输入。
type ExpireGrantRequest struct {
	GrantID          string    `json:"grant_id"`
	ExpectedRevision int64     `json:"expected_revision"`
	ExpiredAt        time.Time `json:"expired_at"`
}

// AccessRequest 描述一次需要 capability 判断的受保护读取或发送请求。
type AccessRequest struct {
	RequestID          string        `json:"request_id"`
	GrantID            string        `json:"grant_id"`
	CallerPrincipalRef string        `json:"caller_principal_ref"`
	ScopeRef           string        `json:"scope_ref"`
	Capability         string        `json:"capability"`
	Source             FenceSnapshot `json:"source"`
	Target             FenceSnapshot `json:"target"`
	RequestDigest      string        `json:"request_digest"`
	RequestedAt        time.Time     `json:"requested_at"`
}

// AccessAuthorization 是 owner 对 AccessRequest 的单次判决，不提供隐式升级能力。
type AccessAuthorization struct {
	RequestID     string      `json:"request_id"`
	GrantID       string      `json:"grant_id"`
	Capability    string      `json:"capability"`
	GrantRevision int64       `json:"grant_revision"`
	AuthorizedAt  time.Time   `json:"authorized_at"`
	ExpiresAt     time.Time   `json:"expires_at"`
	Grant         AccessGrant `json:"grant"`
}

// Authorization 保持旧调用方使用的短名称，但与 AccessAuthorization 完全同型。
type Authorization = AccessAuthorization

// EventSelector 固定 wait 可观察的单一 source/lane/target 范围。
type EventSelector struct {
	Version       int64           `json:"version"`
	SourceID      string          `json:"source_id"`
	SourceEpoch   string          `json:"source_epoch"`
	Lane          eventstore.Lane `json:"lane"`
	TargetAgentID string          `json:"target_agent_id"`
	EventTypes    []string        `json:"event_types"`
}

// WaitRequest 创建 durable wait；WaitHandleID 可为空，表示由 server 生成随机 opaque id。
type WaitRequest struct {
	WaitHandleID         string        `json:"wait_handle_id,omitempty"`
	GrantID              string        `json:"grant_id"`
	GrantRevision        int64         `json:"grant_revision"`
	CallerPrincipalRef   string        `json:"caller_principal_ref"`
	ScopeRef             string        `json:"scope_ref"`
	Selector             EventSelector `json:"selector"`
	AfterSequence        int64         `json:"after_sequence"`
	AfterCursor          string        `json:"after_cursor,omitempty"`
	DeadlineAt           time.Time     `json:"deadline_at"`
	IdempotencyNamespace string        `json:"idempotency_namespace"`
	IdempotencyKeyDigest string        `json:"idempotency_key_digest"`
	RequestDigest        string        `json:"request_digest"`
	// waitHandleIDGenerated is internal bookkeeping used to make server-minted
	// ids replayable by idempotency key without exposing a caller-controlled
	// bearer field on the wire.
	waitHandleIDGenerated bool
}

// WaitHandle 是 wait 的 durable 查询句柄；它没有授权含义，读取时仍需当前 grant。
type WaitHandle struct {
	WaitHandleID       string `json:"wait_handle_id"`
	GrantID            string `json:"grant_id"`
	GrantRevision      int64  `json:"grant_revision"`
	CallerPrincipalRef string `json:"caller_principal_ref"`
	ScopeRef           string `json:"scope_ref"`
	SourceAgentID      string `json:"source_agent_id"`
	// Target is copied from the grant and is revalidated on every read. It is
	// a fence snapshot, never a bearer capability.
	Target                 FenceSnapshot `json:"target"`
	Selector               EventSelector `json:"selector"`
	AfterSequence          int64         `json:"after_sequence"`
	AfterCursor            string        `json:"after_cursor,omitempty"`
	DeadlineAt             time.Time     `json:"deadline_at"`
	IdempotencyNamespace   string        `json:"idempotency_namespace"`
	IdempotencyKeyDigest   string        `json:"idempotency_key_digest"`
	RequestDigest          string        `json:"request_digest"`
	Status                 string        `json:"status"`
	Revision               int64         `json:"revision"`
	MatchedEventID         string        `json:"matched_event_id,omitempty"`
	MatchedSourceSequence  int64         `json:"matched_source_sequence,omitempty"`
	MatchedCanonicalCursor string        `json:"matched_canonical_cursor,omitempty"`
	CreatedAt              time.Time     `json:"created_at"`
	UpdatedAt              time.Time     `json:"updated_at"`
	ResolvedAt             *time.Time    `json:"resolved_at,omitempty"`
}

// PollRequest 只允许句柄 owner 在同一 scope 下继续查询，不接受 selector 替换。
type PollRequest struct {
	WaitHandleID       string `json:"wait_handle_id"`
	CallerPrincipalRef string `json:"caller_principal_ref"`
	ScopeRef           string `json:"scope_ref"`
	GrantRevision      int64  `json:"grant_revision"`
}

// CancelRequest 取消 watcher，不改写已经由 owner 固化的命令或事件事实。
type CancelRequest struct {
	WaitHandleID       string    `json:"wait_handle_id"`
	CallerPrincipalRef string    `json:"caller_principal_ref"`
	ScopeRef           string    `json:"scope_ref"`
	GrantRevision      int64     `json:"grant_revision"`
	ReasonRef          string    `json:"reason_ref"`
	CancelledAt        time.Time `json:"cancelled_at"`
}

// WaitResult 是一次有界轮询结果；timeout/indeterminate 不是业务失败。
type WaitResult struct {
	Handle     WaitHandle             `json:"handle"`
	Event      *eventstore.AgentEvent `json:"event,omitempty"`
	Status     string                 `json:"status"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	IsError    bool                   `json:"is_error"`
}

// IdentityReader 是 wait admission 使用的只读身份窄端口。
type IdentityReader interface {
	GetCurrent(context.Context, string) (identity.CurrentBinding, error)
}

// EventReader 是 wait 使用的 canonical dense event 只读端口；实现不得读取投影缓存。
type EventReader interface {
	ListEvents(context.Context, string, string, eventstore.Lane, int64, int64) ([]eventstore.AgentEvent, error)
}

// EventSourceStatusReader 提供 canonical source 状态；Store 构造器要求该窄口，
// 以便 gap/quarantine/closed 在 wait 读取前必定 fail-closed。
type EventSourceStatusReader interface {
	GetSourceState(context.Context, string, string, eventstore.Lane) (eventstore.SourceState, error)
}

// EventWatcher is an optional same-process wake-up seam. It carries no
// authorization or durability semantics; Wait always retains PollWait as the
// canonical fallback when this seam is absent.
type EventWatcher interface {
	Register(context.Context, eventstore.WatchFilter) (*eventstore.WatchSubscription, error)
}

// TransactionalWaitReader is the additive recovery seam for exact opaque IDs.
type TransactionalWaitReader interface {
	GetWaitByIDTx(context.Context, storedb.DBTX, string) (WaitHandle, error)
}

// AccessWaitOwner 是编排 service 可依赖的窄 owner；实现不得把这些调用旁路到
// direct-peer、report 或内存 bus。正文和 provider effect 由其他 owner 管理。
type AccessWaitOwner interface {
	CreateGrant(context.Context, AccessGrantRequest) (AccessGrant, error)
	GetGrant(context.Context, string) (AccessGrant, error)
	RevokeGrant(context.Context, RevokeGrantRequest) (AccessGrant, error)
	ExpireGrant(context.Context, ExpireGrantRequest) (AccessGrant, error)
	Authorize(context.Context, AccessRequest) (Authorization, error)
	CreateWait(context.Context, WaitRequest) (WaitHandle, error)
	GetWait(context.Context, PollRequest) (WaitHandle, error)
	PollWait(context.Context, PollRequest) (WaitResult, error)
	Wait(context.Context, PollRequest) (WaitResult, error)
	CancelWait(context.Context, CancelRequest) (WaitResult, error)
}

// Validate 检查授权快照的完整代际绑定和有向身份。
func (f FenceSnapshot) Validate() error {
	if err := validateFenceIdentity(f); err != nil {
		return err
	}
	if f.IncarnationID == "" {
		return validateUnboundFence(f)
	}
	return validateBoundFenceFields(f)
}

// ValidateBound 要求可用于跨任务读写授权的完整 active incarnation fence。
// 预代际 reservation 只能用于观察，不能创建或消费跨任务 grant。
func (f FenceSnapshot) ValidateBound() error {
	if err := f.Validate(); err != nil {
		return err
	}
	if f.IncarnationID == "" || f.IncarnationGeneration <= 0 || f.BindingVersion <= 0 || f.BindingDigest == "" {
		return invalidAccess("cross-task access requires a bound incarnation fence")
	}
	return nil
}

// Validate 检查授权申请；申请必须明确 capability、方向、scope、幂等摘要和时间窗。
func (r AccessGrantRequest) Validate() error {
	if err := validateGrantRequestIdentity(r); err != nil {
		return err
	}
	if err := validateDistinctBoundFences(r.Source, r.Target); err != nil {
		return err
	}
	if !r.CanRead && !r.CanSend {
		return invalidAccess("grant must enable read or send")
	}
	return validateGrantRequestProofs(r)
}

// Validate 检查已经持久化的授权及其不可变申请字段。
func (g AccessGrant) Validate() error {
	if err := accessGrantRequestFromGrant(g).Validate(); err != nil {
		return err
	}
	return validatePersistedGrant(g)
}

// Validate 检查撤销请求的 expected revision 和不可篡改审计摘要。
func (r RevokeGrantRequest) Validate() error {
	return validateRevokeGrantRequest(r)
}

// Validate 检查单次 capability 判决请求；capability 只能是 read 或 send。
func (r AccessRequest) Validate() error {
	if err := validateAccessRequestIdentity(r); err != nil {
		return err
	}
	if r.Capability != "read" && r.Capability != "send" {
		return invalidAccess("capability is unsupported")
	}
	if err := validateDistinctBoundFences(r.Source, r.Target); err != nil {
		return err
	}
	return validateAccessRequestProof(r)
}

// Validate 检查授权结果与其 grant、capability 和有效期的一致性。
func (a AccessAuthorization) Validate() error {
	if err := validateAuthorizationIdentity(a); err != nil {
		return err
	}
	if a.Capability != "read" && a.Capability != "send" {
		return invalidAccess("capability is unsupported")
	}
	if err := validateAuthorizationWindow(a); err != nil {
		return err
	}
	if err := a.Grant.Validate(); err != nil {
		return err
	}
	return validateAuthorizationGrant(a)
}

// Validate 固定 selector 判别联合，拒绝跨 source/lane 或重复事件类型。
func (s EventSelector) Validate() error {
	if err := validateSelectorHeader(s); err != nil {
		return err
	}
	return validateSelectorEventTypes(s.EventTypes)
}

// CanonicalJSON 返回 selector 的稳定 JSON 表示，供幂等摘要和数据库字段守卫使用。
func (s EventSelector) CanonicalJSON() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Digest 返回 selector 的 domain-separated SHA-256 摘要；调用方不得把它当授权凭证。
func (s EventSelector) Digest() (string, error) {
	encoded, err := s.CanonicalJSON()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(append([]byte("v3.orchestration.event-selector\x00"), encoded...))
	return hex.EncodeToString(hash[:]), nil
}

// Validate 检查 wait 创建输入；句柄是查询身份，不允许代替 grant 或 selector。
func (r WaitRequest) Validate() error {
	if err := validateWaitRequestIdentity(r); err != nil {
		return err
	}
	if err := r.Selector.Validate(); err != nil {
		return err
	}
	if err := validateWaitCursor(r); err != nil {
		return err
	}
	return validateWaitRequestProof(r)
}

// Validate 检查 durable handle，匹配结果只能引用该 handle 固定的 selector。
func (h WaitHandle) Validate() error {
	if err := waitRequestFromHandle(h).Validate(); err != nil {
		return err
	}
	if err := validateWaitHandleBinding(h); err != nil {
		return err
	}
	if err := validateWaitHandleMetadata(h); err != nil {
		return err
	}
	if err := validateWaitMatch(h); err != nil {
		return err
	}
	return validateWaitResolution(h)
}

// Validate 检查继续轮询请求的 owner、scope 和 grant revision。
func (r PollRequest) Validate() error {
	if err := validateText(ErrInvalidWait, "wait_handle_id", r.WaitHandleID, maxID, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidWait, "caller_principal_ref", r.CallerPrincipalRef, maxRef, false); err != nil {
		return err
	}
	if err := validateText(ErrInvalidWait, "scope_ref", r.ScopeRef, maxRef, false); err != nil {
		return err
	}
	if r.GrantRevision <= 0 {
		return invalidWait("grant_revision must be positive")
	}
	return nil
}

// Validate 检查取消请求；取消是 watcher 生命周期动作，不是事件 terminal 状态改判。
func (r CancelRequest) Validate() error {
	poll := PollRequest{WaitHandleID: r.WaitHandleID, CallerPrincipalRef: r.CallerPrincipalRef, ScopeRef: r.ScopeRef, GrantRevision: r.GrantRevision}
	if err := poll.Validate(); err != nil {
		return err
	}
	if err := validateText(ErrInvalidWait, "reason_ref", r.ReasonRef, maxRef, false); err != nil {
		return err
	}
	if r.CancelledAt.IsZero() {
		return invalidWait("cancelled_at is required")
	}
	return nil
}

// Validate 检查结果状态、句柄状态和可选事件的一致性。
func (r WaitResult) Validate() error {
	if err := r.Handle.Validate(); err != nil {
		return err
	}
	if err := validateWaitResultState(r); err != nil {
		return err
	}
	if err := validateWaitResultCursor(r.NextCursor); err != nil {
		return err
	}
	if err := validateWaitResultEvent(r); err != nil {
		return err
	}
	return validateWaitResultEventStatus(r)
}

// SelectorTargetAgentID 返回结果句柄绑定的 target，供 result 级别做不越界检查。
func (r WaitResult) SelectorTargetAgentID() string { return r.Handle.Selector.TargetAgentID }

func validWaitStatus(status string) bool {
	switch status {
	case "pending", "satisfied", "timed_out", "cancelled", "revoked", "expired":
		return true
	default:
		return false
	}
}

func validWaitResultStatus(status string) bool {
	return validWaitStatus(status) || status == "indeterminate"
}

func invalidAccess(message string) error { return fmt.Errorf("%w: %s", ErrInvalidAccess, message) }
func invalidWait(message string) error   { return fmt.Errorf("%w: %s", ErrInvalidWait, message) }

// validateText 拒绝隐式 trim、NUL、非法 UTF-8 和超长输入，确保请求摘要稳定。
func validateText(kind error, name, value string, limit int, allowEmpty bool) error {
	if !allowEmpty && value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", kind, name)
	}
	return nil
}

// validateDigest 只接受与 SQLite schema 一致的小写 256-bit hex，不做隐式前缀归一化。
func validateDigest(kind error, name, value string, required bool) error {
	if !required && value == "" {
		return nil
	}
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be lowercase 256-bit hex", kind, name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%w: %s is not hexadecimal", kind, name)
	}
	return nil
}

// validateTimeWindow 要求授权时间窗为 UTC、非空且严格递增。
func validateTimeWindow(kind error, issued, expires time.Time) error {
	if issued.IsZero() || expires.IsZero() || issued.Location() != time.UTC || expires.Location() != time.UTC || !expires.After(issued) {
		return fmt.Errorf("%w: issued_at/expires_at time window is invalid", kind)
	}
	return nil
}
