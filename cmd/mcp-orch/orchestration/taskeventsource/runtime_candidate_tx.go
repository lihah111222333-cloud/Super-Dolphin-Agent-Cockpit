package taskeventsource

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/identity"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

var (
	// ErrRuntimeEventTxCandidateInvalid 标识事务候选输入或 owner 违反了窄合同。
	ErrRuntimeEventTxCandidateInvalid = errors.New("runtime event transaction candidate is invalid")
	// ErrRuntimeEventTxCandidateGap 标识 source 尚有未解决的 dense sequence gap。
	ErrRuntimeEventTxCandidateGap = errors.New("runtime event transaction candidate source has a gap")
	// ErrRuntimeEventTxCandidateCommitUnknown 标识 callback 成功后事务提交结果不可判定。
	ErrRuntimeEventTxCandidateCommitUnknown = errors.New("runtime event transaction candidate commit outcome is unknown")
	// ErrRuntimeEventTxCandidateFenced 标识候选已因不确定提交进入 query-only 状态。
	ErrRuntimeEventTxCandidateFenced = errors.New("runtime event transaction candidate is fenced")
)

// RuntimeEventTransactionOwner 是唯一事务 owner；callback 必须收到同一个
// transaction-bound DBTX，并在 callback 返回错误时回滚。owner 不得把 *sql.DB
// 传给 callback，也不得在提交结果不确定时让调用方重放 callback。
type RuntimeEventTransactionOwner interface {
	WithTransaction(context.Context, func(storedb.DBTX) error) error
}

// RuntimeEventTransactionOwnerFunc 将显式事务 callback 收窄为 owner 接口。
type RuntimeEventTransactionOwnerFunc func(context.Context, func(storedb.DBTX) error) error

// WithTransaction 执行函数 owner；nil 函数直接失败而不触达数据库。
func (f RuntimeEventTransactionOwnerFunc) WithTransaction(ctx context.Context, callback func(storedb.DBTX) error) error {
	if f == nil {
		return fmt.Errorf("%w: transaction owner is nil", ErrRuntimeEventTxCandidateInvalid)
	}
	if callback == nil {
		return fmt.Errorf("%w: transaction callback is nil", ErrRuntimeEventTxCandidateInvalid)
	}
	return f(ctx, callback)
}

// RuntimeEventTransactionIdentityReader 在给定 DBTX 上读取完整 identity 快照。
// 快照必须包含 reservation、incarnation 与 current binding 的同事务证明。
type RuntimeEventTransactionIdentityReader interface {
	GetActiveSnapshotTx(context.Context, storedb.DBTX, string) (identity.ActiveSnapshot, error)
}

// RuntimeEventTransactionSourceReader 在同一 DBTX 上读取 source/lane 水位。
type RuntimeEventTransactionSourceReader interface {
	GetSourceStateTx(context.Context, storedb.DBTX, string, string, eventstore.Lane) (eventstore.SourceState, error)
}

// RuntimeEventTransactionAppender 在同一 DBTX 上接收 canonical frame。
// 实现不得自行开启事务、分配 sequence 或写 canonical event 以外的 owner。
type RuntimeEventTransactionAppender interface {
	AppendTx(context.Context, storedb.DBTX, eventstore.IngressFrame) (eventstore.AppendResult, error)
}

// RuntimeEventTxCandidate 是 transport-neutral 的 identity/event 原子接缝。
// 它不注册 Fx/provider/tool，也不拥有数据库；唯一写入来自 transaction appender。
type RuntimeEventTxCandidate struct {
	owner      RuntimeEventTransactionOwner
	identities RuntimeEventTransactionIdentityReader
	states     RuntimeEventTransactionSourceReader
	appender   RuntimeEventTransactionAppender

	mu     sync.Mutex
	fenced bool
}

var _ RuntimeEventTransactionOwner = RuntimeEventTransactionOwnerFunc(nil)

// NewRuntimeEventTxCandidate 创建未注册的事务候选；所有依赖都必须显式提供。
func NewRuntimeEventTxCandidate(
	owner RuntimeEventTransactionOwner,
	identities RuntimeEventTransactionIdentityReader,
	states RuntimeEventTransactionSourceReader,
	appender RuntimeEventTransactionAppender,
) (*RuntimeEventTxCandidate, error) {
	if isNilRuntimeEventTxDependency(owner) || isNilRuntimeEventTxDependency(identities) ||
		isNilRuntimeEventTxDependency(states) || isNilRuntimeEventTxDependency(appender) {
		return nil, fmt.Errorf("%w: transaction owner, readers and appender are required", ErrRuntimeEventTxCandidateInvalid)
	}
	return &RuntimeEventTxCandidate{owner: owner, identities: identities, states: states, appender: appender}, nil
}

// Append 在同一个 transaction-bound DBTX 内读取 identity/current/source 并接收 frame。
// source sequence 只复用 durable NextSequence；候选不创建 source、不递增水位，也不猜测序列一。
func (c *RuntimeEventTxCandidate) Append(ctx context.Context, event RuntimeEvent) (eventstore.AppendResult, error) {
	if err := validateRuntimeEventTxAppend(c, ctx, event); err != nil {
		return eventstore.AppendResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fenced {
		return eventstore.AppendResult{}, errors.Join(ErrRuntimeEventTxCandidateFenced, ErrRuntimeEventTxCandidateCommitUnknown)
	}
	return c.finishRuntimeEventTxAttempt(c.runRuntimeEventTxAttempt(ctx, event))
}

type runtimeEventTxAttempt struct {
	result            eventstore.AppendResult
	callbackRan       bool
	callbackSucceeded bool
	callbackErr       error
	txErr             error
}

// runRuntimeEventTxAttempt 在 owner callback 中执行一次完整 candidate 事务尝试。
func (c *RuntimeEventTxCandidate) runRuntimeEventTxAttempt(ctx context.Context, event RuntimeEvent) runtimeEventTxAttempt {
	var attempt runtimeEventTxAttempt
	attempt.txErr = c.owner.WithTransaction(ctx, func(txdb storedb.DBTX) error {
		attempt.callbackRan = true
		if err := validateRuntimeEventTxDBTX(txdb); err != nil {
			attempt.callbackErr = err
			return err
		}
		attempt.result, attempt.callbackErr = c.appendTx(ctx, txdb, event)
		attempt.callbackErr = classifyRuntimeEventTxError(attempt.callbackErr)
		attempt.callbackSucceeded = attempt.callbackErr == nil
		return attempt.callbackErr
	})
	return attempt
}

// finishRuntimeEventTxAttempt 处理回滚、提交未知和 owner 未调用 callback 的异常结果。
func (c *RuntimeEventTxCandidate) finishRuntimeEventTxAttempt(attempt runtimeEventTxAttempt) (eventstore.AppendResult, error) {
	if attempt.txErr == nil && !attempt.callbackRan {
		return eventstore.AppendResult{}, fmt.Errorf("%w: transaction owner did not invoke callback", ErrRuntimeEventTxCandidateInvalid)
	}
	if attempt.txErr == nil && attempt.callbackRan && attempt.callbackErr == nil {
		return attempt.result, nil
	}
	if runtimeEventTxAttemptIsUnknown(attempt) {
		c.fenced = true
		return eventstore.AppendResult{}, runtimeEventTxUnknownError(attempt)
	}
	return eventstore.AppendResult{}, attempt.txErr
}

// runtimeEventTxAttemptIsUnknown 判断 callback 成功但事务结果未被 owner 证明的窗口。
func runtimeEventTxAttemptIsUnknown(attempt runtimeEventTxAttempt) bool {
	return attempt.callbackSucceeded || errors.Is(attempt.txErr, ErrRuntimeEventTxCandidateCommitUnknown) ||
		(attempt.txErr == nil && attempt.callbackRan && attempt.callbackErr != nil) ||
		(attempt.callbackRan && attempt.callbackErr == nil)
}

func runtimeEventTxUnknownError(attempt runtimeEventTxAttempt) error {
	if errors.Is(attempt.txErr, ErrRuntimeEventTxCandidateCommitUnknown) {
		return attempt.txErr
	}
	cause := attempt.txErr
	if cause == nil {
		cause = attempt.callbackErr
	}
	return errors.Join(ErrRuntimeEventTxCandidateCommitUnknown, cause)
}

// appendTx 执行所有只读 fence、frame 构造和唯一 tx-bound appender 调用。
func (c *RuntimeEventTxCandidate) appendTx(ctx context.Context, txdb storedb.DBTX, event RuntimeEvent) (eventstore.AppendResult, error) {
	source, input, err := c.buildRuntimeEventTxInput(ctx, txdb, event)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	frame, err := buildFrame(source, input)
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	if err := frame.Validate(); err != nil {
		return eventstore.AppendResult{}, err
	}
	return c.appendRuntimeEventTxFrame(ctx, txdb, frame)
}

// buildRuntimeEventTxInput 在单一 DBTX 上重读身份与水位并构造事件输入。
func (c *RuntimeEventTxCandidate) buildRuntimeEventTxInput(ctx context.Context, txdb storedb.DBTX, event RuntimeEvent) (Source, EventInput, error) {
	snapshot, err := c.identities.GetActiveSnapshotTx(ctx, txdb, event.AgentID)
	if err != nil {
		return Source{}, EventInput{}, fmt.Errorf("read active identity snapshot in transaction: %w", err)
	}
	source, err := FromCurrentBinding(snapshot.Binding)
	if err != nil {
		return Source{}, EventInput{}, err
	}
	if source.AgentID != event.AgentID {
		return Source{}, EventInput{}, fmt.Errorf("%w: identity snapshot agent does not match runtime event", ErrRuntimeEventTxCandidateInvalid)
	}
	state, err := c.states.GetSourceStateTx(ctx, txdb, source.SourceID, source.SourceEpoch, event.Lane)
	if err != nil {
		return Source{}, EventInput{}, errors.Join(ErrRuntimeEventCandidateUnavailable, fmt.Errorf("read source state in transaction: %w", err))
	}
	if err := validateRuntimeEventTxSourceState(state, source, event); err != nil {
		return Source{}, EventInput{}, err
	}
	input, err := buildRuntimeEventInput(snapshot, state, event)
	if err != nil {
		return Source{}, EventInput{}, err
	}
	source, err = validateIdentitySnapshots(input)
	if err != nil {
		return Source{}, EventInput{}, err
	}
	return source, input, nil
}

func (c *RuntimeEventTxCandidate) appendRuntimeEventTxFrame(ctx context.Context, txdb storedb.DBTX, frame eventstore.IngressFrame) (eventstore.AppendResult, error) {
	result, err := c.appender.AppendTx(ctx, txdb, frame)
	if errors.Is(err, eventstore.ErrEventGap) {
		return eventstore.AppendResult{}, errors.Join(ErrRuntimeEventTxCandidateGap, err)
	}
	if err != nil {
		return eventstore.AppendResult{}, err
	}
	return result, nil
}

// validateRuntimeEventTxSourceState 只接受已有 active 水位；gap、坏水位和 fence 漂移均阻断写入。
func validateRuntimeEventTxSourceState(state eventstore.SourceState, source Source, event RuntimeEvent) error {
	if err := validateRuntimeEventTxSourceIdentity(state, source, event); err != nil {
		return err
	}
	if err := validateRuntimeEventTxSourceWatermarks(state); err != nil {
		return err
	}
	return validateRuntimeEventTxSourceStatus(state.Status)
}

func validateRuntimeEventTxSourceIdentity(state eventstore.SourceState, source Source, event RuntimeEvent) error {
	if state.SourceID == source.SourceID && state.SourceEpoch == source.SourceEpoch && state.Lane == event.Lane && state.WriterEpoch == event.WriterEpoch {
		return nil
	}
	return errors.Join(ErrRuntimeEventCandidateUnavailable, errors.New("source state fence does not match runtime event"))
}

// validateRuntimeEventTxSourceWatermarks 拒绝无法证明单调关系的持久水位。
func validateRuntimeEventTxSourceWatermarks(state eventstore.SourceState) error {
	if state.NextSequence > 0 && state.MaterializedSequence >= 0 && state.DedupFloor >= 0 && state.Revision >= 1 && state.MaterializedSequence < state.NextSequence && state.DedupFloor <= state.MaterializedSequence {
		return nil
	}
	return errors.Join(ErrRuntimeEventCandidateUnavailable, errors.New("source state watermarks are invalid"))
}

func validateRuntimeEventTxSourceStatus(status string) error {
	switch status {
	case "active":
		return nil
	case "gap":
		return errors.Join(ErrRuntimeEventTxCandidateGap, eventstore.ErrEventGap)
	default:
		return errors.Join(ErrRuntimeEventCandidateUnavailable, errors.New("source state is not active"))
	}
}

// classifyRuntimeEventTxError 把 appender 返回的 dense gap 固定为候选拒绝类别。
func classifyRuntimeEventTxError(err error) error {
	if errors.Is(err, eventstore.ErrEventGap) && !errors.Is(err, ErrRuntimeEventTxCandidateGap) {
		return errors.Join(ErrRuntimeEventTxCandidateGap, err)
	}
	return err
}

// validateRuntimeEventTxAppend 在开启事务前拒绝 nil 依赖、context、agent 和 lane。
func validateRuntimeEventTxAppend(candidate *RuntimeEventTxCandidate, ctx context.Context, event RuntimeEvent) error {
	if isNilRuntimeEventTxDependency(candidate) || isNilRuntimeEventTxDependency(ctx) ||
		isNilRuntimeEventTxDependency(candidate.owner) || isNilRuntimeEventTxDependency(candidate.identities) ||
		isNilRuntimeEventTxDependency(candidate.states) || isNilRuntimeEventTxDependency(candidate.appender) {
		return fmt.Errorf("%w: candidate dependencies or context are missing", ErrRuntimeEventTxCandidateInvalid)
	}
	if event.AgentID == "" {
		return fmt.Errorf("%w: runtime event agent id is required", ErrRuntimeEventTxCandidateInvalid)
	}
	switch event.Lane {
	case eventstore.LaneControl, eventstore.LaneOutput, eventstore.LaneContent:
		return nil
	default:
		return fmt.Errorf("%w: runtime event lane is unsupported", ErrRuntimeEventTxCandidateInvalid)
	}
}

// validateRuntimeEventTxDBTX 拒绝裸 *sql.DB/*sql.Conn，防止 reader/appender 越过 owner 事务。
func validateRuntimeEventTxDBTX(txdb storedb.DBTX) error {
	if isNilRuntimeEventTxDependency(txdb) {
		return fmt.Errorf("%w: transaction-bound DBTX is required", ErrRuntimeEventTxCandidateInvalid)
	}
	switch txdb.(type) {
	case *sql.DB, *sql.Conn:
		return fmt.Errorf("%w: callback DBTX is not transaction-bound", ErrRuntimeEventTxCandidateInvalid)
	default:
		return nil
	}
}

// isNilRuntimeEventTxDependency 检测 interface 中的 typed-nil，避免候选运行期 panic。
func isNilRuntimeEventTxDependency(value any) bool {
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
