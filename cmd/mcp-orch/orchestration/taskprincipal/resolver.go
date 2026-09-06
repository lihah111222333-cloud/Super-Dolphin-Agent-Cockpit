// Package taskprincipal 将 transport 私有调用归因解析为当前 task 身份链。
package taskprincipal

import (
	"context"
	"errors"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

var (
	// ErrTaskPrincipalUnavailable 表示当前调用缺少可信归因或身份 owner 无法完成读取。
	ErrTaskPrincipalUnavailable = errors.New("task principal is unavailable")
	// ErrTaskPrincipalFence 表示 transport 归因与当前 durable 身份链不再一致。
	ErrTaskPrincipalFence = errors.New("task principal fence comparison failed")
	// ErrTaskPrincipalInvalid 表示 resolver 或 transport 私有身份快照无效。
	ErrTaskPrincipalInvalid = errors.New("task principal is invalid")
)

// IdentityChainReader 是 task principal consumer 拥有的最小身份读取端口。
type IdentityChainReader interface {
	Get(ctx context.Context, reservationID string) (identity.Reservation, error)
	GetIncarnation(ctx context.Context, incarnationID string) (identity.Incarnation, error)
	GetCurrent(ctx context.Context, agentID string) (identity.CurrentBinding, error)
}

// IdentityChainTxReader 是在调用方真实事务中读取完整身份快照的窄端口。
// 它是 additive candidate seam；普通 RequireCurrent API 保持原有兼容行为。
type IdentityChainTxReader interface {
	GetActiveSnapshotTx(context.Context, storedb.DBTX, string) (identity.ActiveSnapshot, error)
}

// TaskPrincipal 是同一次读取中完成交叉验证的调用者、归因和当前 binding 快照。
type TaskPrincipal struct {
	Principal   common.CallerPrincipal
	Attribution common.CallerAttribution
	Binding     identity.CurrentBinding
}

// Resolver 只读取 durable identity owner，不签发 principal 或接受客户端 metadata。
type Resolver struct {
	reader IdentityChainReader
}

// New 构造 fail-closed task principal resolver，nil 与 typed-nil reader 都被拒绝。
func New(reader IdentityChainReader) (*Resolver, error) {
	if identityChainReaderIsNil(reader) {
		return nil, ErrTaskPrincipalInvalid
	}
	return &Resolver{reader: reader}, nil
}

// RequireCurrent 按 reservation、incarnation、current binding 的固定顺序重读并验证身份链。
func (r *Resolver) RequireCurrent(ctx context.Context) (TaskPrincipal, error) {
	if err := r.ready(); err != nil {
		return TaskPrincipal{}, err
	}
	principal, attribution, err := requireTransportIdentity(ctx)
	if err != nil {
		return TaskPrincipal{}, err
	}
	if err := r.requireReservation(ctx, principal, attribution.Source); err != nil {
		return TaskPrincipal{}, err
	}
	if err := r.requireIncarnation(ctx, attribution.Source); err != nil {
		return TaskPrincipal{}, err
	}
	binding, err := r.reader.GetCurrent(ctx, attribution.Source.AgentID)
	if err != nil {
		return TaskPrincipal{}, ErrTaskPrincipalUnavailable
	}
	if binding.State != "active" || executionFenceFromBinding(binding) != attribution.Source {
		return TaskPrincipal{}, ErrTaskPrincipalFence
	}
	return TaskPrincipal{Principal: principal, Attribution: attribution, Binding: binding}, nil
}

// RequireCurrentTx 在调用方已持有的真实事务中完成 task principal 身份链读取。
// 不会创建嵌套事务，也不会退回到 split-read API。
func (r *Resolver) RequireCurrentTx(ctx context.Context, dbtx storedb.DBTX) (TaskPrincipal, error) {
	if err := r.ready(); err != nil {
		return TaskPrincipal{}, err
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return TaskPrincipal{}, err
	}
	reader, err := r.transactionReader()
	if err != nil {
		return TaskPrincipal{}, ErrTaskPrincipalUnavailable
	}
	principal, attribution, err := requireTransportIdentity(ctx)
	if err != nil {
		return TaskPrincipal{}, err
	}
	snapshot, err := readActiveSnapshotTx(ctx, reader, dbtx, attribution.Source.AgentID)
	if err != nil {
		return TaskPrincipal{}, err
	}
	if err := validateActiveSnapshot(snapshot, principal, attribution.Source); err != nil {
		return TaskPrincipal{}, err
	}
	return TaskPrincipal{Principal: principal, Attribution: attribution, Binding: snapshot.Binding}, nil
}

// transactionReader 取得支持调用方事务读取的 identity reader，不创建隐式替代实现。
func (r *Resolver) transactionReader() (IdentityChainTxReader, error) {
	reader, ok := r.reader.(IdentityChainTxReader)
	if !ok {
		return nil, ErrTaskPrincipalUnavailable
	}
	return reader, nil
}

// readActiveSnapshotTx 在调用方事务中读取 active identity snapshot，并折叠 owner 细节。
func readActiveSnapshotTx(ctx context.Context, reader IdentityChainTxReader, dbtx storedb.DBTX, agentID string) (identity.ActiveSnapshot, error) {
	snapshot, err := reader.GetActiveSnapshotTx(ctx, dbtx, agentID)
	if err == nil {
		return snapshot, nil
	}
	if errors.Is(err, identity.ErrIdentityFence) {
		return identity.ActiveSnapshot{}, ErrTaskPrincipalFence
	}
	return identity.ActiveSnapshot{}, ErrTaskPrincipalUnavailable
}

// validateActiveSnapshot 按固定顺序校验事务读取的 reservation、incarnation 与 binding。
func validateActiveSnapshot(snapshot identity.ActiveSnapshot, principal common.CallerPrincipal, source common.ExecutionFence) error {
	if !reservationMatches(snapshot.Reservation, principal, source) {
		return ErrTaskPrincipalFence
	}
	if !incarnationMatches(snapshot.Incarnation, source) {
		return ErrTaskPrincipalFence
	}
	if snapshot.Binding.State != "active" {
		return ErrTaskPrincipalFence
	}
	if executionFenceFromBinding(snapshot.Binding) != source {
		return ErrTaskPrincipalFence
	}
	return nil
}

// ready 检查 resolver 及其 identity reader 是否仍具备可用依赖。
func (r *Resolver) ready() error {
	if r == nil || identityChainReaderIsNil(r.reader) {
		return ErrTaskPrincipalInvalid
	}
	return nil
}

// requireTransportIdentity 从受信上下文读取 principal 与 attribution，拒绝客户端替代值。
func requireTransportIdentity(ctx context.Context) (common.CallerPrincipal, common.CallerAttribution, error) {
	principal, err := common.RequireCallerPrincipal(ctx)
	if err != nil {
		return common.CallerPrincipal{}, common.CallerAttribution{}, classifyPrincipalError(err)
	}
	attribution, err := common.RequireCallerAttribution(ctx)
	if err != nil {
		return common.CallerPrincipal{}, common.CallerAttribution{}, classifyAttributionError(err)
	}
	return principal, attribution, nil
}

// requireReservation 重读并校验 reservation，失败时不泄漏 owner 或 source 细节。
func (r *Resolver) requireReservation(ctx context.Context, principal common.CallerPrincipal, source common.ExecutionFence) error {
	reservation, err := r.reader.Get(ctx, source.ReservationID)
	if err != nil {
		return ErrTaskPrincipalUnavailable
	}
	if !reservationMatches(reservation, principal, source) {
		return ErrTaskPrincipalFence
	}
	return nil
}

// requireIncarnation 重读并校验不可变 incarnation，阻断 stale execution 继续传播。
func (r *Resolver) requireIncarnation(ctx context.Context, source common.ExecutionFence) error {
	incarnation, err := r.reader.GetIncarnation(ctx, source.IncarnationID)
	if err != nil {
		return ErrTaskPrincipalUnavailable
	}
	if !incarnationMatches(incarnation, source) {
		return ErrTaskPrincipalFence
	}
	return nil
}

// reservationMatches 校验 principal、logical agent、reservation 代际和来源 fence。
func reservationMatches(reservation identity.Reservation, principal common.CallerPrincipal, source common.ExecutionFence) bool {
	return reservation.State == "activated" &&
		reservation.CallerPrincipalRef == principal.PrincipalRef &&
		reservation.ScopeRef == principal.ScopeRef &&
		reservation.AgentID == source.AgentID &&
		reservation.ReservationID == source.ReservationID &&
		reservation.ReservationGeneration == source.ReservationGeneration &&
		reservation.SourceFenceRef == source.SourceFenceRef
}

// incarnationMatches 校验不可变 execution incarnation 与 transport source fence。
func incarnationMatches(incarnation identity.Incarnation, source common.ExecutionFence) bool {
	return incarnation.IncarnationID == source.IncarnationID &&
		incarnation.AgentID == source.AgentID &&
		incarnation.ReservationID == source.ReservationID &&
		incarnation.IncarnationGeneration == source.IncarnationGeneration &&
		incarnation.SourceFenceRef == source.SourceFenceRef
}

// executionFenceFromBinding 从当前 binding 显式投影完整 transport execution fence。
func executionFenceFromBinding(binding identity.CurrentBinding) common.ExecutionFence {
	return common.ExecutionFence{
		AgentID:               binding.AgentID,
		ReservationID:         binding.ReservationID,
		ReservationGeneration: binding.ReservationGeneration,
		IncarnationID:         binding.IncarnationID,
		IncarnationGeneration: binding.IncarnationGeneration,
		BindingVersion:        binding.BindingVersion,
		BindingDigest:         binding.BindingDigest,
		SourceFenceRef:        binding.SourceFenceRef,
	}
}

// classifyPrincipalError 将 caller principal 错误折叠为稳定的 resolver 错误。
func classifyPrincipalError(err error) error {
	if errors.Is(err, common.ErrMissingCallerPrincipal) {
		return ErrTaskPrincipalUnavailable
	}
	return ErrTaskPrincipalInvalid
}

// classifyAttributionError 将 transport attribution 错误折叠为稳定的 resolver 错误。
func classifyAttributionError(err error) error {
	if errors.Is(err, common.ErrCallerAttributionUnavailable) {
		return ErrTaskPrincipalUnavailable
	}
	return ErrTaskPrincipalInvalid
}

// identityChainReaderIsNil 拒绝 interface 中携带的 typed-nil identity reader。
func identityChainReaderIsNil(reader IdentityChainReader) bool {
	if reader == nil {
		return true
	}
	value := reflect.ValueOf(reader)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
