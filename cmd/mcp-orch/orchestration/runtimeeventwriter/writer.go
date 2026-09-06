// Package runtimeeventwriter owns the production transaction-bound runtime
// event ingress seam. It accepts observations only after re-reading the active
// identity chain and source watermark in the same transaction used by the
// canonical EventStore append.
package runtimeeventwriter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

var (
	// ErrInvalid marks malformed observations or missing transaction owners.
	ErrInvalid = errors.New("runtime event writer input is invalid")
	// ErrGap marks a source watermark gap that cannot be materialized safely.
	ErrGap = errors.New("runtime event writer source has a gap")
	// ErrStale marks a source, binding, or writer-epoch fence mismatch.
	ErrStale = errors.New("runtime event writer source or binding is stale")
	// ErrCommitUnknown marks a callback whose commit outcome cannot be proven.
	ErrCommitUnknown = errors.New("runtime event writer commit outcome is unknown")
	// ErrFenced marks a writer that refuses replay after an unknown commit.
	ErrFenced = errors.New("runtime event writer is fenced")

	// errCallbackCardinality marks a transaction owner that invoked the
	// callback more than once. It remains private because the runtime writer is
	// a candidate-only seam; callers only rely on the stable ErrInvalid class.
	errCallbackCardinality = errors.New("runtime event writer transaction callback invoked more than once")
)

// TransactionOwner is the sole owner of the write transaction. A successful
// callback must be committed exactly once; an unknown commit fences the writer.
type TransactionOwner interface {
	WithTransaction(context.Context, func(storedb.DBTX) error) error
}

// IdentityReader is the transaction-bound active identity read port.
type IdentityReader interface {
	GetActiveSnapshotTx(context.Context, storedb.DBTX, string) (identity.ActiveSnapshot, error)
}

// Binding is the complete provider execution tuple supplied by a terminal
// observation.  A bound ingress must verify every member against durable state
// before appending in the same owner transaction; no member may be inferred.
type Binding struct {
	AgentID               string `json:"agent_id"`
	IncarnationID         string `json:"incarnation_id"`
	IncarnationGeneration uint64 `json:"incarnation_generation"`
	Provider              string `json:"provider"`
	ProviderSessionID     string `json:"provider_session_id"`
	LaunchSequence        uint64 `json:"launch_sequence"`
}

const bindingTextLimit = 128

// BindingVerifierTx 在 append 事务保持打开时重读完整 provider binding；发生
// rollover、session 过期或 launch sequence 未知时必须 fail-closed。
type BindingVerifierTx interface {
	VerifyTerminalBindingTx(context.Context, storedb.DBTX, Binding) error
}

// SourceReader 是事务绑定的 canonical event source 水位读取端口。
type SourceReader interface {
	GetSourceStateTx(context.Context, storedb.DBTX, string, string, eventstore.Lane) (eventstore.SourceState, error)
}

// Appender is the canonical append owner. Implementations must not open a
// nested transaction or write projections before canonical append succeeds.
type Appender interface {
	AppendTx(context.Context, storedb.DBTX, eventstore.IngressFrame) (eventstore.AppendResult, error)
}

// AllocatedEnvelopeAppender is the canonical production ingress seam. The
// owner allocates sequence/cursor/hash and appends them in the same transaction;
// callers provide only the identity-fenced envelope.
type AllocatedEnvelopeAppender interface {
	AppendAllocatedEnvelopeTx(context.Context, storedb.DBTX, string, string, eventstore.Lane, string, func() (eventstore.EventEnvelope, error)) (eventstore.AppendResult, error)
}

// Ingress is the explicit caller seam for runtime/terminal adapters. A caller
// must supply a fully observed event; the writer supplies identity, sequence,
// cursor and canonical persistence. No provider callback or projection is
// accepted here, so terminal integrations can be added only with durable fence
// context available at the call site.
type Ingress interface {
	Append(context.Context, RuntimeEvent) (eventstore.AppendResult, error)
}

// BoundIngress 是 terminal observation 的原子 ingress seam；verifier 与 append
// 必须由同一事务 owner 执行，单独的 context-only 校验不具备授权能力。
type BoundIngress interface {
	AppendBound(context.Context, RuntimeEvent, BindingVerifierTx) (eventstore.AppendResult, error)
}

// RuntimeEvent is an observation with no caller-controlled identity, sequence,
// cursor, or envelope hash. Those values are reconstructed from durable owners.
type RuntimeEvent struct {
	Binding     Binding
	AgentID     string
	Lane        eventstore.Lane
	EventID     string
	EventType   string
	Provenance  string
	TrustClass  string
	ContentRole string
	ContentRef  string
	ContentSize int64
	MediaType   string
	ProviderAt  *string
	ObservedAt  string
	WriterEpoch string
}

// Event is retained as a concise alias for RuntimeEvent.
type Event = RuntimeEvent

// Writer 负责在事务绑定上下文中校验 runtime event 并执行 canonical append。
type Writer struct {
	owner    TransactionOwner
	identity IdentityReader
	source   SourceReader
	appender Appender

	mu     sync.Mutex
	fenced bool
}

// RuntimeEventWriter is the descriptive public name for Writer.
type RuntimeEventWriter = Writer

// New 构造生产 runtime event writer；所有 owner 端口必须显式提供，typed-nil 依赖立即失败。
func New(owner TransactionOwner, identityReader IdentityReader, sourceReader SourceReader, appender Appender) (*Writer, error) {
	if isNil(owner) || isNil(identityReader) || isNil(sourceReader) || isNil(appender) {
		return nil, fmt.Errorf("%w: transaction owner, readers and appender are required", ErrInvalid)
	}
	return &Writer{owner: owner, identity: identityReader, source: sourceReader, appender: appender}, nil
}

// NewWriter 是偏好领域名称的依赖注入调用方使用的显式构造别名。
func NewWriter(owner TransactionOwner, identityReader IdentityReader, sourceReader SourceReader, appender Appender) (*Writer, error) {
	return New(owner, identityReader, sourceReader, appender)
}

// Append 在真实事务中执行 identity/source 围栏与 canonical append，并对未知提交永久封锁重放。
func (w *Writer) Append(ctx context.Context, event Event) (eventstore.AppendResult, error) {
	if err := validateCall(w, ctx, event); err != nil {
		return eventstore.AppendResult{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrFenced, ErrCommitUnknown)
	}
	return w.appendLocked(ctx, event)
}

// AppendBound 在同一 owner 事务中校验完整 provider binding 并追加事件；缺少事务校验器时立即失败。
func (w *Writer) AppendBound(ctx context.Context, event Event, verifier BindingVerifierTx) (eventstore.AppendResult, error) {
	if err := validateCall(w, ctx, event); err != nil {
		return eventstore.AppendResult{}, err
	}
	if isNil(verifier) {
		return eventstore.AppendResult{}, fmt.Errorf("%w: transactional binding verifier is required", ErrInvalid)
	}
	if err := validateBinding(event.Binding); err != nil {
		return eventstore.AppendResult{}, err
	}
	if event.AgentID != event.Binding.AgentID {
		return eventstore.AppendResult{}, fmt.Errorf("%w: event agent id does not match binding", ErrInvalid)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrFenced, ErrCommitUnknown)
	}
	return w.resolveTransaction(w.runTransactionWithVerifier(ctx, event, verifier))
}

type transactionOutcome struct {
	result            eventstore.AppendResult
	txErr             error
	callbackErr       error
	callbackRan       bool
	callbackSucceeded bool
	callbackCalls     uint8
}

// appendLocked 在持有 writer 锁时运行唯一事务，并把提交结果交给围栏解析器。
func (w *Writer) appendLocked(ctx context.Context, event Event) (eventstore.AppendResult, error) {
	outcome := w.runTransaction(ctx, event)
	return w.resolveTransaction(outcome)
}

// runTransaction 让 owner 执行至多一次有效回调，并保留 callback 与提交错误的原始关系。
func (w *Writer) runTransaction(ctx context.Context, event Event) transactionOutcome {
	return w.runTransactionWithVerifier(ctx, event, nil)
}

func (w *Writer) runTransactionWithVerifier(ctx context.Context, event Event, verifier BindingVerifierTx) transactionOutcome {
	var outcome transactionOutcome
	var callbackGuard transactionCallbackGuard
	outcome.txErr = w.owner.WithTransaction(ctx, func(txdb storedb.DBTX) error {
		if err := callbackGuard.admit(); err != nil {
			return errors.Join(ErrInvalid, err)
		}
		outcome.callbackRan = true
		if err := validateTxDBTX(txdb); err != nil {
			outcome.callbackErr = err
			return err
		}
		if verifier != nil {
			if err := verifier.VerifyTerminalBindingTx(ctx, txdb, event.Binding); err != nil {
				outcome.callbackErr = err
				return outcome.callbackErr
			}
		}
		outcome.result, outcome.callbackErr = w.appendTx(ctx, txdb, event)
		outcome.callbackSucceeded = outcome.callbackErr == nil
		return outcome.callbackErr
	})
	outcome.callbackCalls = callbackGuard.count()
	return outcome
}

// resolveTransaction 将 owner 的回调次数、提交状态和业务错误映射为 fail-closed 结果。
func (w *Writer) resolveTransaction(outcome transactionOutcome) (eventstore.AppendResult, error) {
	if outcome.callbackCalls > 1 {
		// This narrow owner interface has no rollback receipt. Even a non-nil
		// owner error may follow a committed first callback, so duplicate
		// invocation is always treated as an unknown outcome and fences replay.
		w.fenced = true
		return eventstore.AppendResult{}, errors.Join(ErrCommitUnknown, ErrInvalid, errCallbackCardinality, outcome.txErr)
	}
	if outcome.txErr == nil && outcome.callbackRan && outcome.callbackErr == nil {
		return outcome.result, nil
	}
	if !outcome.callbackRan {
		return w.resolveMissingCallback(outcome.txErr)
	}
	if shouldFenceAfterCallback(outcome) {
		w.fenced = true
		cause := outcome.txErr
		if cause == nil {
			cause = outcome.callbackErr
		}
		return eventstore.AppendResult{}, errors.Join(ErrCommitUnknown, cause)
	}
	return eventstore.AppendResult{}, outcome.txErr
}

func shouldFenceAfterCallback(outcome transactionOutcome) bool {
	if outcome.callbackSucceeded {
		return true
	}
	if errors.Is(outcome.txErr, eventstore.ErrTransactionCommitUnknown) {
		return true
	}
	return outcome.txErr == nil && outcome.callbackErr != nil
}

func (w *Writer) resolveMissingCallback(txErr error) (eventstore.AppendResult, error) {
	if errors.Is(txErr, eventstore.ErrTransactionCommitUnknown) {
		w.fenced = true
		return eventstore.AppendResult{}, errors.Join(ErrCommitUnknown, txErr)
	}
	if txErr != nil {
		return eventstore.AppendResult{}, txErr
	}
	return eventstore.AppendResult{}, fmt.Errorf("%w: transaction owner did not invoke callback", ErrInvalid)
}

// transactionCallbackGuard bounds owner callback invocations to one accepted
// execution plus one duplicate observation. Additional invocations remain
// rejected without unbounded counter growth or business callback execution.
type transactionCallbackGuard struct {
	mu    sync.Mutex
	calls uint8
}

func (g *transactionCallbackGuard) admit() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.calls > 0 {
		g.calls = 2
		return errCallbackCardinality
	}
	g.calls = 1
	return nil
}

func (g *transactionCallbackGuard) count() uint8 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// appendTx 在同一 DBTX 中重读 identity/source，并选择 owner 分配或旧式 frame append 路径。
func (w *Writer) appendTx(ctx context.Context, txdb storedb.DBTX, event Event) (eventstore.AppendResult, error) {
	snapshot, err := w.identity.GetActiveSnapshotTx(ctx, txdb, event.AgentID)
	if err != nil {
		return eventstore.AppendResult{}, fmt.Errorf("read active identity snapshot in transaction: %w", err)
	}
	source, err := taskeventsource.FromCurrentBinding(snapshot.Binding)
	if err != nil {
		return eventstore.AppendResult{}, errors.Join(ErrStale, err)
	}
	if source.AgentID != event.AgentID {
		return eventstore.AppendResult{}, fmt.Errorf("%w: identity snapshot agent does not match event", ErrStale)
	}
	if event.Binding.AgentID != "" {
		if err := validateSnapshotBinding(snapshot, event.Binding); err != nil {
			return eventstore.AppendResult{}, err
		}
	}
	if allocator, ok := w.appender.(AllocatedEnvelopeAppender); ok {
		return w.appendAllocatedTx(ctx, txdb, snapshot, source, event, allocator)
	}
	return w.appendLegacyTx(ctx, txdb, snapshot, source, event)
}

// validateBinding 校验 provider binding 的关键身份字段完整且具备有效代际与启动序号。
func validateBinding(binding Binding) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "agent_id", value: binding.AgentID},
		{name: "incarnation_id", value: binding.IncarnationID},
		{name: "provider", value: binding.Provider},
		{name: "provider_session_id", value: binding.ProviderSessionID},
	} {
		if err := validateBindingText(field.name, field.value); err != nil {
			return err
		}
	}
	if binding.IncarnationGeneration == 0 || binding.LaunchSequence == 0 {
		return fmt.Errorf("%w: incarnation generation and launch sequence must be positive", ErrInvalid)
	}
	return nil
}

// validateBindingText 校验 binding 标识为有界、规范且与时区无关的 UTF-8 文本。
func validateBindingText(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > bindingTextLimit ||
		!utf8.ValidString(value) || strings.ContainsRune(value, 0) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: %s must be bounded canonical UTF-8 text", ErrInvalid, name)
	}
	return nil
}

func validateSnapshotBinding(snapshot identity.ActiveSnapshot, binding Binding) error {
	current := snapshot.Binding
	if current.IncarnationGeneration <= 0 || current.AgentID != binding.AgentID || current.IncarnationID != binding.IncarnationID || uint64(current.IncarnationGeneration) != binding.IncarnationGeneration {
		return errors.Join(ErrStale, eventstore.ErrEventFence)
	}
	return nil
}

// appendAllocatedTx 保留 identity 快照后调用 owner 分配序列、cursor 和 hash，并验证回执绑定。
func (w *Writer) appendAllocatedTx(ctx context.Context, txdb storedb.DBTX, snapshot identity.ActiveSnapshot, source taskeventsource.Source, event Event, allocator AllocatedEnvelopeAppender) (eventstore.AppendResult, error) {
	if _, err := taskeventsource.ValidateIdentitySnapshots(taskeventsource.EventInput{
		Reservation: snapshot.Reservation, Incarnation: snapshot.Incarnation, Binding: snapshot.Binding,
		SubjectAgentID: snapshot.Binding.AgentID,
	}); err != nil {
		return eventstore.AppendResult{}, errors.Join(ErrStale, err)
	}
	envelope, err := buildEnvelope(snapshot, event)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	result, err := allocator.AppendAllocatedEnvelopeTx(ctx, txdb, source.SourceID, source.SourceEpoch, event.Lane, event.WriterEpoch, func() (eventstore.EventEnvelope, error) {
		return envelope, nil
	})
	if errors.Is(err, eventstore.ErrEventGap) {
		// Preserve the committed-error marker so the owner can persist gap evidence.
		return result, errors.Join(ErrGap, err)
	}
	if err != nil {
		return result, err
	}
	if err := validateAllocatedResult(result, source.SourceID, source.SourceEpoch, event.Lane, envelope); err != nil {
		return eventstore.AppendResult{}, err
	}
	return result, nil
}

// appendLegacyTx 构造不带 owner allocator 的 frame，沿用 source 水位和 canonical append 守卫。
func (w *Writer) appendLegacyTx(ctx context.Context, txdb storedb.DBTX, snapshot identity.ActiveSnapshot, source taskeventsource.Source, event Event) (eventstore.AppendResult, error) {
	state, err := w.source.GetSourceStateTx(ctx, txdb, source.SourceID, source.SourceEpoch, event.Lane)
	if err != nil {
		return eventstore.AppendResult{}, errors.Join(ErrStale, err)
	}
	if err := validateState(state, source, event); err != nil {
		return eventstore.AppendResult{}, err
	}
	input, err := buildInput(snapshot, state, event)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if _, err := taskeventsource.ValidateIdentitySnapshots(input); err != nil {
		return eventstore.AppendResult{}, errors.Join(ErrStale, err)
	}
	frame, err := taskeventsource.BuildFrame(source, input)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := frame.Validate(); err != nil {
		return eventstore.AppendResult{}, err
	}
	result, err := w.appender.AppendTx(ctx, txdb, frame)
	if errors.Is(err, eventstore.ErrEventGap) {
		return result, errors.Join(ErrGap, err)
	}
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	return result, nil
}

// validateAllocatedResult 逐项证明 allocator 返回的 receipt、event 和 state 属于本次 envelope。
func validateAllocatedResult(result eventstore.AppendResult, sourceID, sourceEpoch string, lane eventstore.Lane, envelope eventstore.EventEnvelope) error {
	hash, err := eventstore.CanonicalEnvelopeHash(envelope)
	if err != nil {
		return errors.Join(ErrStale, err)
	}
	if err := validateAllocatedReceipt(result, sourceID, sourceEpoch, lane, envelope, hash); err != nil {
		return err
	}
	if err := validateAllocatedCursor(result.Receipt, sourceID, sourceEpoch, lane); err != nil {
		return err
	}
	if err := validateAllocatedEvent(result.Event, result.Receipt, sourceID, sourceEpoch, lane, envelope, hash); err != nil {
		return err
	}
	return validateAllocatedState(result.State, sourceID, sourceEpoch, lane)
}

// validateAllocatedReceipt 证明 receipt 已 materialize 且绑定本次 source 与 envelope。
func validateAllocatedReceipt(result eventstore.AppendResult, sourceID, sourceEpoch string, lane eventstore.Lane, envelope eventstore.EventEnvelope, hash string) error {
	receipt := result.Receipt
	if receipt.Status != "materialized" || result.Event == nil {
		return errors.Join(ErrStale, errors.New("allocator result is not materialized"))
	}
	if receipt.SourceID != sourceID || receipt.SourceEpoch != sourceEpoch || receipt.Lane != lane {
		return errors.Join(ErrStale, errors.New("allocator result does not bind source and envelope"))
	}
	if !reflect.DeepEqual(receipt.Envelope, envelope) || receipt.EnvelopeHash != hash || receipt.SourceSequence < 1 {
		return errors.Join(ErrStale, errors.New("allocator result does not bind source and envelope"))
	}
	return nil
}

func validateAllocatedCursor(receipt eventstore.IngressReceipt, sourceID, sourceEpoch string, lane eventstore.Lane) error {
	expectedCursor, err := eventstore.CanonicalCursor(sourceID, sourceEpoch, lane, receipt.SourceSequence)
	if err != nil || receipt.CanonicalCursor != expectedCursor {
		return errors.Join(ErrStale, errors.New("allocator result cursor is invalid"))
	}
	return nil
}

// validateAllocatedEvent 证明 materialized event 与 receipt 共享 source、sequence 和 envelope。
func validateAllocatedEvent(event *eventstore.AgentEvent, receipt eventstore.IngressReceipt, sourceID, sourceEpoch string, lane eventstore.Lane, envelope eventstore.EventEnvelope, hash string) error {
	if event == nil || event.Status != "materialized" {
		return errors.Join(ErrStale, errors.New("allocator result is not materialized"))
	}
	if event.SourceID != sourceID || event.SourceEpoch != sourceEpoch || event.Lane != lane {
		return errors.Join(ErrStale, errors.New("allocator result event is not bound to receipt"))
	}
	if event.SourceSequence != receipt.SourceSequence || !reflect.DeepEqual(event.Envelope, envelope) || event.EnvelopeHash != hash {
		return errors.Join(ErrStale, errors.New("allocator result event is not bound to receipt"))
	}
	return nil
}

// validateAllocatedState 证明 allocator 返回的 source state 仍为 active 且水位前进。
func validateAllocatedState(state eventstore.SourceState, sourceID, sourceEpoch string, lane eventstore.Lane) error {
	if state.SourceID != sourceID || state.SourceEpoch != sourceEpoch || state.Lane != lane {
		return errors.Join(ErrStale, errors.New("allocator result state is invalid"))
	}
	if state.Status != "active" || state.NextSequence <= state.MaterializedSequence {
		return errors.Join(ErrStale, errors.New("allocator result state is invalid"))
	}
	if state.MaterializedSequence < 0 || state.DedupFloor < 0 || state.Revision < 1 {
		return errors.Join(ErrStale, errors.New("allocator result state is invalid"))
	}
	return nil
}

func buildEnvelope(snapshot identity.ActiveSnapshot, event Event) (eventstore.EventEnvelope, error) {
	incarnationID := snapshot.Incarnation.IncarnationID
	envelope := eventstore.EventEnvelope{EventID: event.EventID, EventType: event.EventType, SchemaVersion: 1,
		RootAgentID: snapshot.Reservation.AgentID, SubjectAgentID: snapshot.Binding.AgentID,
		ReservationID: snapshot.Reservation.ReservationID, IncarnationID: &incarnationID,
		ExternalEpoch: snapshot.Incarnation.ExternalEpoch, IncarnationGeneration: snapshot.Incarnation.IncarnationGeneration,
		IdentityDigest: snapshot.Incarnation.IdentityDigest, BindingVersion: snapshot.Binding.BindingVersion,
		BindingDigest: snapshot.Binding.BindingDigest, SourceFenceRef: snapshot.Binding.SourceFenceRef,
		Provenance: event.Provenance, TrustClass: event.TrustClass, ContentRole: event.ContentRole,
		ContentRef: event.ContentRef, ContentSize: event.ContentSize, MediaType: event.MediaType,
		ProviderAt: clone(event.ProviderAt), ObservedAt: event.ObservedAt, WriterEpoch: event.WriterEpoch}
	if err := envelope.Validate(); err != nil {
		return eventstore.EventEnvelope{}, err
	}
	return envelope, nil
}

// validateState 拒绝 source tuple、writer epoch、水位或状态无法证明安全性的写入。
func validateState(state eventstore.SourceState, source taskeventsource.Source, event Event) error {
	if err := validateStateBinding(state, source, event); err != nil {
		return err
	}
	if err := validateStateWatermarks(state); err != nil {
		return err
	}
	return validateStateStatus(state.Status)
}

func validateStateBinding(state eventstore.SourceState, source taskeventsource.Source, event Event) error {
	if state.SourceID != source.SourceID || state.SourceEpoch != source.SourceEpoch || state.Lane != event.Lane || state.WriterEpoch != event.WriterEpoch {
		return errors.Join(ErrStale, eventstore.ErrEventFence)
	}
	return nil
}

// validateStateWatermarks 拒绝无法证明连续性的负数、倒退或 dedup 水位。
func validateStateWatermarks(state eventstore.SourceState) error {
	if state.NextSequence <= 0 || state.MaterializedSequence < 0 || state.DedupFloor < 0 {
		return errors.Join(ErrStale, errors.New("source state watermarks are invalid"))
	}
	if state.Revision < 1 || state.MaterializedSequence >= state.NextSequence || state.DedupFloor > state.MaterializedSequence {
		return errors.Join(ErrStale, errors.New("source state watermarks are invalid"))
	}
	return nil
}

func validateStateStatus(status string) error {
	if status == "gap" {
		return errors.Join(ErrGap, eventstore.ErrEventGap)
	}
	if status != "active" {
		return errors.Join(ErrStale, eventstore.ErrEventFence)
	}
	return nil
}

func buildInput(snapshot identity.ActiveSnapshot, state eventstore.SourceState, event Event) (taskeventsource.EventInput, error) {
	// buildInput 仅从 durable snapshot 和 source 水位构造事件，不采信调用方序列或摘要。
	incarnationID := snapshot.Incarnation.IncarnationID
	envelope := eventstore.EventEnvelope{EventID: event.EventID, EventType: event.EventType, SchemaVersion: 1,
		RootAgentID: snapshot.Reservation.AgentID, SubjectAgentID: snapshot.Binding.AgentID,
		ReservationID: snapshot.Reservation.ReservationID, IncarnationID: &incarnationID,
		ExternalEpoch: snapshot.Incarnation.ExternalEpoch, IncarnationGeneration: snapshot.Incarnation.IncarnationGeneration,
		IdentityDigest: snapshot.Incarnation.IdentityDigest, BindingVersion: snapshot.Binding.BindingVersion,
		BindingDigest: snapshot.Binding.BindingDigest, SourceFenceRef: snapshot.Binding.SourceFenceRef,
		Provenance: event.Provenance, TrustClass: event.TrustClass, ContentRole: event.ContentRole,
		ContentRef: event.ContentRef, ContentSize: event.ContentSize, MediaType: event.MediaType,
		ProviderAt: clone(event.ProviderAt), ObservedAt: event.ObservedAt, WriterEpoch: event.WriterEpoch}
	if err := envelope.Validate(); err != nil {
		return taskeventsource.EventInput{}, err
	}
	hash, err := CanonicalEnvelopeHash(envelope)
	if err != nil {
		return taskeventsource.EventInput{}, err
	}
	return taskeventsource.EventInput{Reservation: snapshot.Reservation, Incarnation: snapshot.Incarnation, Binding: snapshot.Binding,
		Lane: event.Lane, SourceSequence: state.NextSequence, EnvelopeHash: hash, EventID: event.EventID,
		EventType: event.EventType, SubjectAgentID: snapshot.Binding.AgentID, Provenance: event.Provenance, TrustClass: event.TrustClass,
		ContentRole: event.ContentRole, ContentRef: event.ContentRef, ContentSize: event.ContentSize, MediaType: event.MediaType,
		ProviderAt: clone(event.ProviderAt), ObservedAt: event.ObservedAt, WriterEpoch: event.WriterEpoch}, nil
}

// CanonicalEnvelopeHash 计算 ingress receipt 使用的稳定小写 SHA-256；sequence 和 cursor 刻意排除在信封外。
func CanonicalEnvelopeHash(envelope eventstore.EventEnvelope) (string, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("marshal runtime event envelope: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// validateCall 在获取锁和开启事务前拦截缺失依赖、空 agent 与未知 lane。
func validateCall(w *Writer, ctx context.Context, event Event) error {
	if err := validateCallDependencies(w, ctx); err != nil {
		return err
	}
	if event.AgentID == "" {
		return fmt.Errorf("%w: agent id is required", ErrInvalid)
	}
	return validateLane(event.Lane)
}

// validateCallDependencies 在进入锁与事务前确认 writer、owner、读端口和上下文均非空。
func validateCallDependencies(w *Writer, ctx context.Context) error {
	if isNil(w) || isNil(ctx) || isNil(w.owner) || isNil(w.identity) || isNil(w.source) || isNil(w.appender) {
		return fmt.Errorf("%w: writer dependencies or context are missing", ErrInvalid)
	}
	return nil
}

func validateLane(lane eventstore.Lane) error {
	switch lane {
	case eventstore.LaneControl, eventstore.LaneOutput, eventstore.LaneContent:
	default:
		return fmt.Errorf("%w: unsupported lane", ErrInvalid)
	}
	return nil
}

func validateTxDBTX(txdb storedb.DBTX) error {
	// validateTxDBTX 只接受 sqlctx 能证明属于真实事务的 DBTX。
	if err := sqlctx.RequireTransactionDBTX(txdb); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

func clone(value *string) *string {
	// clone 复制可选 provider 时间，避免 frame 持有调用方可变指针。
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func isNil(value any) bool {
	// isNil 检测 interface 中的 typed-nil，避免构造或调用阶段 panic。
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

var _ TransactionOwner = (*eventstore.Store)(nil)
var _ IdentityReader = (*identity.Store)(nil)
var _ SourceReader = (*eventstore.Store)(nil)
var _ Appender = (*eventstore.Store)(nil)
var _ Ingress = (*Writer)(nil)
var _ BoundIngress = (*Writer)(nil)
