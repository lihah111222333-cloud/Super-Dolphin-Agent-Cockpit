// Package eventstore 提供 orch 的 canonical dense event 持久化 owner。
package eventstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const maxEnvelopeBytes = 256 * 1024

var (
	ErrInvalidEvent       = errors.New("event input is invalid")
	ErrEventConflict      = errors.New("event identity conflicts with existing receipt")
	ErrEventNotFound      = errors.New("event record not found")
	ErrEventGap           = errors.New("event source sequence has a gap")
	ErrEventQuarantined   = errors.New("event source is quarantined")
	ErrEventFence         = errors.New("event source fence comparison failed")
	ErrEventReplayExpired = errors.New("event replay is below dedup floor")
	// ErrEventRetentionPinned 表示仍有读取游标依赖待保留的事件历史。
	ErrEventRetentionPinned = errors.New("event retention pin blocks tombstone")
)

// FenceVerifier 只读取并验证 reservation/incarnation/binding fence，不写数据库或调用 provider。
type FenceVerifier interface {
	VerifyEventFence(context.Context, EventEnvelope) error
}

// CanonicalEnvelopeHash 计算 ingress receipt 使用的稳定小写 SHA-256 摘要。
// 摘要故意不包含 source sequence 与 canonical cursor。
func CanonicalEnvelopeHash(envelope EventEnvelope) (string, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal canonical event envelope: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// IngressFenceVerifier 是可选的完整 ingress 验证端口。
//
// FenceVerifier 的历史窄接口只接收 envelope，无法观察 source tuple。实现了
// 此接口的 owner verifier 还会核对 frame.SourceID/SourceEpoch，Store 会在
// envelope 验证后调用它；旧的 envelope-only verifier 仍可用于合法的系统/观察
// 分支，但不能绕过 IngressFrame.Validate 对 bound task source 的结构性校验。
type IngressFenceVerifier interface {
	VerifyIngressFrameFence(context.Context, IngressFrame) error
}

// IngressFenceVerifierTx 在调用方持有的同一事务中验证完整 ingress fence。
// 生产 allocator 只接受该接口，避免 identity 读取落到事务之外形成 TOCTOU。
type IngressFenceVerifierTx interface {
	VerifyIngressFrameFenceTx(context.Context, storedb.DBTX, IngressFrame) error
}

// RetentionPinVerifier 是墓碑推进前的事务内保留校验窄接口。
// 实现必须只读取调用方提供的 DBTX；它不得开启嵌套事务、写入其他 owner
// 或调用 provider。未配置时保留现有 EventStore 行为，配置后任何错误均阻断。
type RetentionPinVerifier interface {
	VerifyRetentionPinTx(context.Context, storedb.DBTX, TombstoneRequest) error
}

// RetentionPinReader 是未来 wait/cursor owner 可实现的只读事实端口。
// EventStore 不依赖具体 wait 包，避免把 accesswait/waitoperation 引入存储层。
type RetentionPinReader interface {
	HasRetentionPinTx(context.Context, storedb.DBTX, TombstoneRequest) (bool, error)
}

// Lane 是独立推进的 control、output 或 content 序列。
type Lane string

const (
	LaneControl Lane = "control"
	LaneOutput  Lane = "output"
	LaneContent Lane = "content"
)

// EventEnvelope 是不内联正文的 canonical 事件信封。
type EventEnvelope struct {
	EventID               string  `json:"event_id"`
	EventType             string  `json:"event_type"`
	SchemaVersion         int64   `json:"schema_version"`
	RootAgentID           string  `json:"root_agent_id"`
	SubjectAgentID        string  `json:"subject_agent_id"`
	ReservationID         string  `json:"reservation_id"`
	IncarnationID         *string `json:"incarnation_id"`
	ExternalEpoch         string  `json:"external_epoch"`
	IncarnationGeneration int64   `json:"incarnation_generation"`
	IdentityDigest        string  `json:"identity_digest"`
	BindingVersion        int64   `json:"binding_version"`
	BindingDigest         string  `json:"binding_digest"`
	SourceFenceRef        string  `json:"source_fence_ref"`
	Provenance            string  `json:"provenance"`
	TrustClass            string  `json:"trust_class"`
	ContentRole           string  `json:"content_role"`
	ContentRef            string  `json:"content_ref"`
	ContentSize           int64   `json:"content_size"`
	MediaType             string  `json:"media_type"`
	ProviderAt            *string `json:"provider_at"`
	ObservedAt            string  `json:"observed_at"`
	WriterEpoch           string  `json:"writer_epoch"`
}

// IngressFrame 是带 source/lane/sequence 的一次输入候选。
type IngressFrame struct {
	SourceID        string        `json:"source_id"`
	SourceEpoch     string        `json:"source_epoch"`
	Lane            Lane          `json:"lane"`
	SourceSequence  int64         `json:"source_sequence"`
	EnvelopeHash    string        `json:"envelope_hash"`
	CanonicalCursor string        `json:"canonical_cursor"`
	Envelope        EventEnvelope `json:"envelope"`
}

// IngressReceipt 是接收事实；staged 不代表 canonical event 已发布。
type IngressReceipt struct {
	IngressFrame
	Status   string `json:"status"`
	ErrorRef string `json:"error_ref"`
}

// AgentEvent 是已 materialize 的不可变 canonical event。
type AgentEvent struct {
	IngressFrame
	Status string `json:"status"`
}

// SourceState 是 source/lane 的持久水位和隔离状态。
type SourceState struct {
	SourceID             string `json:"source_id"`
	SourceEpoch          string `json:"source_epoch"`
	Lane                 Lane   `json:"lane"`
	NextSequence         int64  `json:"next_sequence"`
	MaterializedSequence int64  `json:"materialized_sequence"`
	DedupFloor           int64  `json:"dedup_floor"`
	Revision             int64  `json:"revision"`
	Status               string `json:"status"`
	GapReason            string `json:"gap_reason"`
	WriterEpoch          string `json:"writer_epoch"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

// AppendResult 同时返回接收回执、可选 canonical event 和最新 source state。
type AppendResult struct {
	Receipt IngressReceipt `json:"receipt"`
	Event   *AgentEvent    `json:"event"`
	State   SourceState    `json:"state"`
}

// MaterializationCursor 是单个投影在某 source/lane 的独立水位。
type MaterializationCursor struct {
	ProjectionID         string `json:"projection_id"`
	SourceID             string `json:"source_id"`
	SourceEpoch          string `json:"source_epoch"`
	Lane                 Lane   `json:"lane"`
	MaterializedSequence int64  `json:"materialized_sequence"`
	MaterializedHash     string `json:"materialized_hash"`
	Revision             int64  `json:"revision"`
	Status               string `json:"status"`
	UpdatedAt            string `json:"updated_at"`
}

// Tombstone 是保留 envelope hash 的 dedup/GC 事实。
type Tombstone struct {
	SourceID        string `json:"source_id"`
	SourceEpoch     string `json:"source_epoch"`
	Lane            Lane   `json:"lane"`
	SourceSequence  int64  `json:"source_sequence"`
	EnvelopeHash    string `json:"envelope_hash"`
	EventID         string `json:"event_id"`
	CanonicalCursor string `json:"canonical_cursor"`
	DedupFloor      int64  `json:"dedup_floor"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
}

// CursorKey 标识一个 source/lane 的投影 cursor。
type CursorKey struct {
	ProjectionID string
	SourceID     string
	SourceEpoch  string
	Lane         Lane
}

// TombstoneRequest 描述按连续 dedup floor 固化的旧事件哈希。
type TombstoneRequest struct {
	SourceID        string `json:"source_id"`
	SourceEpoch     string `json:"source_epoch"`
	Lane            Lane   `json:"lane"`
	SourceSequence  int64  `json:"source_sequence"`
	EnvelopeHash    string `json:"envelope_hash"`
	EventID         string `json:"event_id"`
	CanonicalCursor string `json:"canonical_cursor"`
	ExpectedFloor   int64  `json:"expected_floor"`
	Status          string `json:"status"`
}

// Validate 在 SQL 前严格校验信封，不进行 trim、默认或静默降级。
func (e EventEnvelope) Validate() error {
	if err := validateEnvelopeText(e); err != nil {
		return err
	}
	if e.SchemaVersion != 1 {
		return invalidEvent("schema_version must be 1")
	}
	if err := validateDigest("identity_digest", e.IdentityDigest, e.IncarnationID != nil); err != nil {
		return err
	}
	if err := validateDigest("binding_digest", e.BindingDigest, e.IncarnationID != nil); err != nil {
		return err
	}
	if e.IncarnationGeneration < 0 || e.BindingVersion < 0 {
		return invalidEvent("identity generations cannot be negative")
	}
	if err := validateEnvelopeIdentityShape(e); err != nil {
		return err
	}
	return validateContentShape(e)
}

func validateEnvelopeText(e EventEnvelope) error {
	for _, field := range []struct {
		name, value string
		limit       int
		optional    bool
	}{
		{"event_id", e.EventID, 128, false}, {"event_type", e.EventType, 128, false},
		{"root_agent_id", e.RootAgentID, 128, false}, {"subject_agent_id", e.SubjectAgentID, 128, false},
		{"reservation_id", e.ReservationID, 128, false}, {"external_epoch", e.ExternalEpoch, 128, true},
		{"source_fence_ref", e.SourceFenceRef, 512, false}, {"provenance", e.Provenance, 256, false},
		{"trust_class", e.TrustClass, 64, false}, {"content_ref", e.ContentRef, 512, true},
		{"media_type", e.MediaType, 128, true}, {"observed_at", e.ObservedAt, 64, false},
		{"writer_epoch", e.WriterEpoch, 128, false},
	} {
		if err := validateText(field.name, field.value, field.limit, field.optional); err != nil {
			return err
		}
	}
	if e.ProviderAt != nil {
		if err := validateText("provider_at", *e.ProviderAt, 64, false); err != nil {
			return err
		}
	}
	return nil
}

// validateEnvelopeIdentityShape 确认未绑定信封不会携带伪造 execution fence。
func validateEnvelopeIdentityShape(e EventEnvelope) error {
	if e.IncarnationID == nil {
		return validatePreIncarnationShape(e)
	}
	return validateBoundIncarnationShape(e)
}

// validatePreIncarnationShape 拒绝未绑定信封携带任何代际或 binding 字段。
func validatePreIncarnationShape(e EventEnvelope) error {
	if e.ExternalEpoch != "" || e.IncarnationGeneration != 0 || e.IdentityDigest != "" || e.BindingVersion != 0 || e.BindingDigest != "" {
		return invalidEvent("pre-incarnation envelope contains execution fence")
	}
	return nil
}

// validateBoundIncarnationShape 要求已绑定信封携带完整 execution fence。
func validateBoundIncarnationShape(e EventEnvelope) error {
	if err := validateText("incarnation_id", *e.IncarnationID, 128, false); err != nil {
		return err
	}
	if e.ExternalEpoch == "" || e.IncarnationGeneration <= 0 || e.IdentityDigest == "" || e.BindingVersion <= 0 || e.BindingDigest == "" {
		return invalidEvent("bound envelope lacks complete execution fence")
	}
	return nil
}

// validateContentShape 强制正文只能通过 CONTENT 引用传递，禁止隐式内联。
func validateContentShape(e EventEnvelope) error {
	switch e.ContentRole {
	case "none":
		if e.ContentRef != "" || e.ContentSize != 0 || e.MediaType != "" {
			return invalidEvent("content_role none requires absent content fields")
		}
	case "prompt", "message", "tool", "output", "event":
		if e.ContentRef == "" || e.MediaType == "" || e.ContentSize < 0 {
			return invalidEvent("content role requires content reference, size and media type")
		}
	default:
		return invalidEvent("content_role is unsupported")
	}
	if e.ContentSize < 0 {
		return invalidEvent("content_size cannot be negative")
	}
	return nil
}

// Validate 校验 source/lane/sequence、摘要和信封 cursor 的完整绑定。
func (f IngressFrame) Validate() error {
	if err := validateFrameKey(f); err != nil {
		return err
	}
	if err := f.Envelope.Validate(); err != nil {
		return err
	}
	if err := validateSourceEnvelopeBinding(f); err != nil {
		return err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return invalidEvent("event envelope cannot be encoded")
	}
	if len(raw) > maxEnvelopeBytes {
		return invalidEvent("event envelope exceeds 256KiB")
	}
	return nil
}

// validateCanonicalEnvelopeHash 拒绝格式合法但并非由 envelope 派生的摘要。
// EnvelopeHash 是 ingress receipt、canonical event 和 tombstone 的完整性根，
// append owner 不得信任调用方提供的同长度十六进制字符串。
func validateCanonicalEnvelopeHash(f IngressFrame) error {
	expected, err := CanonicalEnvelopeHash(f.Envelope)
	if err != nil {
		return invalidEvent("event envelope hash cannot be computed")
	}
	if f.EnvelopeHash != expected {
		return invalidEvent("envelope_hash does not match canonical envelope")
	}
	return nil
}

// validateSourceEnvelopeBinding 将 bound execution event 的 source tuple 固定到
// canonical task source。未绑定的 reservation/provider/system observation 没有
// incarnation epoch，沿用显式的 pre-incarnation 分支；但它们不能冒充 task/*。
func validateSourceEnvelopeBinding(f IngressFrame) error {
	incarnationID := f.Envelope.IncarnationID
	if incarnationID == nil {
		if strings.HasPrefix(f.SourceID, "task/") {
			return sourceBindingError("pre-incarnation envelope cannot use canonical task source")
		}
		return nil
	}
	if strings.ContainsAny(f.Envelope.RootAgentID, "/\\") {
		return sourceBindingError("bound task agent id must be a safe source component")
	}
	if f.Envelope.RootAgentID != f.Envelope.SubjectAgentID {
		return sourceBindingError("bound envelope root and subject agents must match")
	}
	if f.SourceID != "task/"+f.Envelope.RootAgentID {
		return sourceBindingError("bound frame source does not match task agent")
	}
	if f.SourceEpoch != *incarnationID {
		return sourceBindingError("bound frame epoch does not match incarnation")
	}
	return nil
}

// sourceBindingError 保留 malformed-frame 与 stale-fence 两种调用方可识别的
// 归类：输入映射读侧需要 ErrInvalidEvent，写 owner 的安全审计需要 ErrEventFence。
func sourceBindingError(message string) error {
	return errors.Join(ErrEventFence, invalidEvent(message))
}

// validateFrameKey 校验 source tuple、序列号、摘要和确定性 cursor。
func validateFrameKey(f IngressFrame) error {
	expectedCursor, err := CanonicalCursor(f.SourceID, f.SourceEpoch, f.Lane, f.SourceSequence)
	if err != nil {
		return err
	}
	if err := validateDigest("envelope_hash", f.EnvelopeHash, true); err != nil {
		return err
	}
	if err := validateText("canonical_cursor", f.CanonicalCursor, 512, false); err != nil {
		return err
	}
	if f.CanonicalCursor != expectedCursor {
		return invalidEvent("canonical_cursor does not match source tuple")
	}
	return nil
}

// CanonicalCursor 校验 source tuple 与正序 sequence，并生成 wire-compatible 稳定 cursor。
func CanonicalCursor(sourceID, sourceEpoch string, lane Lane, sequence int64) (string, error) {
	if err := validateSourceTuple(sourceID, sourceEpoch, lane); err != nil {
		return "", err
	}
	if strings.IndexFunc(sourceID, unicode.IsControl) >= 0 || strings.IndexFunc(sourceEpoch, unicode.IsControl) >= 0 {
		return "", invalidEvent("cursor source tuple contains control text")
	}
	if sequence <= 0 {
		return "", invalidEvent("source_sequence must be positive")
	}
	return canonicalCursor(sourceID, sourceEpoch, lane, sequence), nil
}

func canonicalCursor(sourceID, epoch string, lane Lane, sequence int64) string {
	return fmt.Sprintf("%s/%s/%s/%d", sourceID, epoch, lane, sequence)
}

// validateText 校验事件字段的规范文本，不做 trim 或默认值替换。
func validateText(name, value string, limit int, allowEmpty bool) error {
	if !allowEmpty && value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return invalidEvent(fmt.Sprintf("%s is not canonical bounded text", name))
	}
	return nil
}

// validateDigest 校验小写 256-bit 摘要；可选字段只能显式为空。
func validateDigest(name, value string, required bool) error {
	if !required && value == "" {
		return nil
	}
	if len(value) != 64 || strings.ToLower(value) != value {
		return invalidEvent(fmt.Sprintf("%s must be lowercase 256-bit hex", name))
	}
	if _, err := hex.DecodeString(value); err != nil {
		return invalidEvent(fmt.Sprintf("%s is not hexadecimal", name))
	}
	return nil
}

func validLane(lane Lane) bool {
	return lane == LaneControl || lane == LaneOutput || lane == LaneContent
}

func invalidEvent(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidEvent, message)
}

// Validate 校验 projection cursor 的身份和水位输入。
func (k CursorKey) Validate() error {
	if err := validateText("projection_id", k.ProjectionID, 128, false); err != nil {
		return err
	}
	if err := validateText("source_id", k.SourceID, 256, false); err != nil {
		return err
	}
	if err := validateText("source_epoch", k.SourceEpoch, 128, false); err != nil {
		return err
	}
	if !validLane(k.Lane) {
		return invalidEvent("lane is unsupported")
	}
	return nil
}

// Validate 校验 tombstone 的连续水位和不可变引用。
func (r TombstoneRequest) Validate() error {
	expectedCursor, err := CanonicalCursor(r.SourceID, r.SourceEpoch, r.Lane, r.SourceSequence)
	if err != nil {
		return err
	}
	if r.ExpectedFloor < 0 {
		return invalidEvent("tombstone floor is invalid")
	}
	if err := validateDigest("envelope_hash", r.EnvelopeHash, true); err != nil {
		return err
	}
	if err := validateText("event_id", r.EventID, 128, false); err != nil {
		return err
	}
	if r.CanonicalCursor != expectedCursor {
		return invalidEvent("tombstone cursor does not match source tuple")
	}
	if r.Status != "materialized" && r.Status != "expired" && r.Status != "quarantined" {
		return invalidEvent("tombstone status is unsupported")
	}
	return nil
}

func validateSourceTuple(sourceID, sourceEpoch string, lane Lane) error {
	if err := validateText("source_id", sourceID, 256, false); err != nil {
		return err
	}
	if err := validateText("source_epoch", sourceEpoch, 128, false); err != nil {
		return err
	}
	if !validLane(lane) {
		return invalidEvent("lane is unsupported")
	}
	return nil
}
