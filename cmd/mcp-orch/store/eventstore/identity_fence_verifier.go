package eventstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// IdentityFenceVerifier 只读取 identity owner，核对事件信封的完整身份栅栏。
type IdentityFenceVerifier struct {
	store IdentityReader
}

var _ FenceVerifier = (*IdentityFenceVerifier)(nil)
var _ IngressFenceVerifier = (*IdentityFenceVerifier)(nil)

// IdentityReader 是验证器读取 reservation、incarnation 和 current binding 的窄端口。
type IdentityReader interface {
	Get(context.Context, string) (identity.Reservation, error)
	GetIncarnation(context.Context, string) (identity.Incarnation, error)
	GetCurrent(context.Context, string) (identity.CurrentBinding, error)
}

// IdentityTxReader 是在调用方真实事务中读取 identity owner 三段记录的窄端口。
// 该接口仅供 additive candidate seam 使用，不改变 envelope-only 兼容接口。
type IdentityTxReader interface {
	GetWithDBTX(context.Context, storedb.DBTX, string) (identity.Reservation, error)
	GetIncarnationWithDBTX(context.Context, storedb.DBTX, string) (identity.Incarnation, error)
	GetCurrentWithDBTX(context.Context, storedb.DBTX, string) (identity.CurrentBinding, error)
}

// NewIdentityFenceVerifier 创建只读身份验证器；缺少 owner 时立即失败。
func NewIdentityFenceVerifier(store IdentityReader) (*IdentityFenceVerifier, error) {
	if identityReaderIsNil(store) {
		return nil, errors.New("identity fence verifier requires identity store")
	}
	return &IdentityFenceVerifier{store: store}, nil
}

// identityReaderIsNil 拒绝带 typed-nil 的只读接口，防止运行期解引用崩溃。
func identityReaderIsNil(reader IdentityReader) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Func, reflect.Slice, reflect.Chan:
		return value.IsNil()
	default:
		return false
	}
}

// VerifyEventFence 验证 reservation、incarnation 和当前 binding 的逐字段一致性。
func (v *IdentityFenceVerifier) VerifyEventFence(ctx context.Context, envelope EventEnvelope) error {
	if v == nil || identityReaderIsNil(v.store) {
		return errors.New("identity fence verifier dependencies are missing")
	}
	reservation, err := v.store.Get(ctx, envelope.ReservationID)
	if err != nil {
		return identityFenceError("reservation lookup", err)
	}
	if err := matchReservationEnvelope(reservation, envelope); err != nil {
		return err
	}
	if envelope.IncarnationID == nil {
		return nil
	}
	return v.verifyBoundEventFence(ctx, envelope, reservation)
}

// VerifyIngressFrameFence 在 envelope fence 之外核对 canonical task source tuple。
// 该检查位于 Store 的完整 frame 入口，防止仅拿到 Appender 的调用方把同一
// reservation/incarnation 伪装成另一个 source stream。
func (v *IdentityFenceVerifier) VerifyIngressFrameFence(ctx context.Context, frame IngressFrame) error {
	if v == nil || identityReaderIsNil(v.store) {
		return errors.New("identity fence verifier dependencies are missing")
	}
	if err := validateSourceEnvelopeBinding(frame); err != nil {
		return err
	}
	return v.VerifyEventFence(ctx, frame.Envelope)
}

// VerifyEventFenceTx 在调用方已持有的真实事务中验证完整 event fence。
// 不创建嵌套事务，也不退回 split-read verifier。
func (v *IdentityFenceVerifier) VerifyEventFenceTx(ctx context.Context, dbtx storedb.DBTX, envelope EventEnvelope) error {
	if v == nil || identityReaderIsNil(v.store) {
		return errors.New("identity fence verifier dependencies are missing")
	}
	if ctx == nil {
		return errors.New("identity fence verifier transaction context is required")
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return err
	}
	reader, ok := v.store.(IdentityTxReader)
	if !ok {
		return errors.New("identity fence verifier transactional reader is unavailable")
	}
	reservation, err := reader.GetWithDBTX(ctx, dbtx, envelope.ReservationID)
	if err != nil {
		return identityFenceError("reservation lookup", err)
	}
	if err := matchReservationEnvelope(reservation, envelope); err != nil {
		return err
	}
	if envelope.IncarnationID == nil {
		return nil
	}
	return v.verifyBoundEventFenceTx(ctx, dbtx, reader, envelope, reservation)
}

// VerifyIngressFrameFenceTx 在同一事务中核对 source tuple 与 identity fence。
func (v *IdentityFenceVerifier) VerifyIngressFrameFenceTx(ctx context.Context, dbtx storedb.DBTX, frame IngressFrame) error {
	if err := validateSourceEnvelopeBinding(frame); err != nil {
		return err
	}
	return v.VerifyEventFenceTx(ctx, dbtx, frame.Envelope)
}

// matchReservationEnvelope 确认事件根 agent、reservation 来源与生命周期分支一致。
func matchReservationEnvelope(reservation identity.Reservation, envelope EventEnvelope) error {
	if reservation.ReservationGeneration <= 0 || reservation.AgentID != envelope.RootAgentID || reservation.SourceFenceRef == "" || reservation.SourceFenceRef != envelope.SourceFenceRef {
		return identityFenceError("reservation fence", nil)
	}
	// An activated reservation is necessarily backed by the first immutable
	// incarnation/current-binding transaction.  Allowing an unbound envelope
	// after that point would let a caller open a second, non-task source stream
	// under an already active identity and bypass the execution fence.  Terminal
	// reservation states intentionally remain admissible here: their
	// pre-incarnation lifecycle events (rejected/failed/expired) are durable
	// facts and must remain queryable.
	if envelope.IncarnationID == nil && reservation.State == "activated" {
		return identityFenceError("activated reservation requires incarnation fence", nil)
	}
	return nil
}

// verifyBoundEventFence 读取并比较不可变 incarnation 与 active current binding。
func (v *IdentityFenceVerifier) verifyBoundEventFence(ctx context.Context, envelope EventEnvelope, reservation identity.Reservation) error {
	incarnation, err := v.store.GetIncarnation(ctx, *envelope.IncarnationID)
	if err != nil {
		return identityFenceError("incarnation lookup", err)
	}
	if !incarnationMatchesEnvelope(incarnation, reservation, envelope) {
		return identityFenceError("incarnation fence", nil)
	}
	current, err := v.store.GetCurrent(ctx, envelope.SubjectAgentID)
	if err != nil {
		return identityFenceError("current binding lookup", err)
	}
	if !currentBindingMatchesEnvelope(current, reservation, incarnation, envelope) {
		return identityFenceError("current binding fence", nil)
	}
	return nil
}

func (v *IdentityFenceVerifier) verifyBoundEventFenceTx(ctx context.Context, dbtx storedb.DBTX, reader IdentityTxReader, envelope EventEnvelope, reservation identity.Reservation) error {
	incarnation, err := reader.GetIncarnationWithDBTX(ctx, dbtx, *envelope.IncarnationID)
	if err != nil {
		return identityFenceError("incarnation lookup", err)
	}
	if !incarnationMatchesEnvelope(incarnation, reservation, envelope) {
		return identityFenceError("incarnation fence", nil)
	}
	current, err := reader.GetCurrentWithDBTX(ctx, dbtx, envelope.SubjectAgentID)
	if err != nil {
		return identityFenceError("current binding lookup", err)
	}
	if !currentBindingMatchesEnvelope(current, reservation, incarnation, envelope) {
		return identityFenceError("current binding fence", nil)
	}
	return nil
}

// incarnationMatchesEnvelope 确认事件声明的 incarnation 仍由已激活 reservation 持有。
func incarnationMatchesEnvelope(incarnation identity.Incarnation, reservation identity.Reservation, envelope EventEnvelope) bool {
	return reservation.State == "activated" && incarnationReservationMatches(incarnation, reservation) && incarnationFenceMatches(incarnation, envelope)
}

// incarnationReservationMatches 确认不可变 incarnation 仍属于原 reservation。
func incarnationReservationMatches(incarnation identity.Incarnation, reservation identity.Reservation) bool {
	return incarnation.ReservationID == reservation.ReservationID && incarnation.AgentID == reservation.AgentID &&
		incarnation.IdentityKind == reservation.IdentityKind && incarnation.SourceFenceRef == reservation.SourceFenceRef
}

// incarnationFenceMatches 确认事件携带的代际字段没有漂移。
func incarnationFenceMatches(incarnation identity.Incarnation, envelope EventEnvelope) bool {
	return incarnation.IncarnationGeneration > 0 && envelope.IncarnationGeneration > 0 &&
		incarnation.ExternalEpoch == envelope.ExternalEpoch && incarnation.IncarnationGeneration == envelope.IncarnationGeneration &&
		incarnation.IdentityDigest != "" && incarnation.IdentityDigest == envelope.IdentityDigest
}

// currentBindingMatchesEnvelope 确认当前绑定仍是事件声明的 active 代际。
func currentBindingMatchesEnvelope(current identity.CurrentBinding, reservation identity.Reservation, incarnation identity.Incarnation, envelope EventEnvelope) bool {
	return current.State == "active" && currentIdentityMatches(current, reservation, incarnation, envelope) && currentFenceMatches(current, reservation, incarnation, envelope)
}

// currentIdentityMatches 确认 current 行指向同一 agent/reservation/incarnation。
func currentIdentityMatches(current identity.CurrentBinding, reservation identity.Reservation, incarnation identity.Incarnation, envelope EventEnvelope) bool {
	return current.AgentID == envelope.SubjectAgentID && current.ReservationID == reservation.ReservationID &&
		current.IncarnationID == incarnation.IncarnationID && current.IdentityKind == incarnation.IdentityKind &&
		current.ExternalIdentityRef == incarnation.ExternalIdentityRef
}

// currentFenceMatches 确认 current 的独立代际与 binding 摘要完全匹配信封。
func currentFenceMatches(current identity.CurrentBinding, reservation identity.Reservation, incarnation identity.Incarnation, envelope EventEnvelope) bool {
	return currentReservationGenerationMatches(current, reservation) &&
		currentIncarnationGenerationMatches(current, incarnation) &&
		current.SourceFenceRef == envelope.SourceFenceRef && currentBindingVersionMatches(current, envelope) &&
		currentBindingDigestMatches(current, envelope)
}

// currentReservationGenerationMatches 确认 current 保留独立 reservation 代际且未回退。
func currentReservationGenerationMatches(current identity.CurrentBinding, reservation identity.Reservation) bool {
	return reservation.ReservationGeneration > 0 && current.ReservationGeneration > 0 &&
		current.ReservationGeneration == reservation.ReservationGeneration
}

// currentIncarnationGenerationMatches 确认 current 与不可变 incarnation 代际一致且为正。
func currentIncarnationGenerationMatches(current identity.CurrentBinding, incarnation identity.Incarnation) bool {
	return current.IncarnationGeneration > 0 && current.IncarnationGeneration == incarnation.IncarnationGeneration
}

// currentBindingVersionMatches 确认事件携带的 binding 版本完整且没有漂移。
func currentBindingVersionMatches(current identity.CurrentBinding, envelope EventEnvelope) bool {
	return current.BindingVersion > 0 && envelope.BindingVersion > 0 && current.BindingVersion == envelope.BindingVersion
}

// currentBindingDigestMatches 确认事件携带的 binding 摘要完整且没有漂移。
func currentBindingDigestMatches(current identity.CurrentBinding, envelope EventEnvelope) bool {
	return current.BindingDigest != "" && envelope.BindingDigest != "" && current.BindingDigest == envelope.BindingDigest
}

// identityFenceError 将底层读取失败或字段漂移归类为稳定 fence 错误。
func identityFenceError(stage string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s mismatch", ErrEventFence, stage)
	}
	return fmt.Errorf("%w: %s: %w", ErrEventFence, stage, cause)
}
