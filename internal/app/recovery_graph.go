package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func allowedRecoveryConstructors() []string {
	return []string{
		"recovery.state",
		"recovery.check",
		"recovery.retry",
		"recovery.restore",
	}
}

// RecoveryConstructorIDs 返回 frozen Recovery graph 的精确构造器顺序。
func RecoveryConstructorIDs() []string {
	return allowedRecoveryConstructors()
}

// ValidateRecoveryConstructors 拒绝新增、缺失、重复或乱序的 Recovery 构造器。
func ValidateRecoveryConstructors(constructors []string) error {
	allowed := allowedRecoveryConstructors()
	if !slices.Equal(constructors, allowed) {
		return fmt.Errorf("Recovery constructors %v do not match frozen allowlist %v", constructors, allowed)
	}
	return nil
}

// RecoveryStateService 只暴露 selector 已验证的持久状态投影。
type RecoveryStateService struct {
	selection StartupSelection
}

// Projection 返回不可变 Recovery 投影。
func (service RecoveryStateService) Projection() RecoveryProjection {
	return service.selection.Projection
}

// RecoveryCheckService 对 current target 执行 exact release 检查。
type RecoveryCheckService struct {
	selection StartupSelection
}

// Check 验证 Recovery transaction 的 current target 与 journal 候选摘要一致。
func (service RecoveryCheckService) Check(ctx context.Context) error {
	transaction, err := requireRecoveryTransaction(ctx, service.selection)
	if err != nil {
		return err
	}
	if transaction.State != recovery.StateProbation && transaction.State != recovery.StateCommitPending {
		return fmt.Errorf("Recovery check is unavailable from state %q", transaction.State)
	}
	digest, err := recovery.ComputeReleaseDigestContext(ctx, transaction.Paths.Target)
	if err != nil {
		return err
	}
	if digest != transaction.Identity.CandidateRelease.SHA256 {
		return fmt.Errorf("%w: Recovery check found candidate digest mismatch", contract.ErrUpdateIntegrityInvalid)
	}
	return nil
}

// RecoveryRetryService 只重放 journal 已持久化的当前意图。
type RecoveryRetryService struct {
	selection StartupSelection
}

// Retry 重放 exact transaction，不推断或创建新状态真值。
func (service RecoveryRetryService) Retry(ctx context.Context) (recovery.Transaction, error) {
	transaction, err := requireRecoveryTransaction(ctx, service.selection)
	if err != nil {
		return recovery.Transaction{}, err
	}
	switch transaction.State {
	case recovery.StateBackupPending, recovery.StateCommitPending, recovery.StateRollbackPending:
	default:
		return recovery.Transaction{}, fmt.Errorf("Recovery retry is unavailable from state %q", transaction.State)
	}
	return service.selection.Store.Replay(ctx, transaction.Identity)
}

// RecoveryRestoreService 按 journal state 终止未安装事务或 exact 回滚已保留的旧 release。
type RecoveryRestoreService struct {
	selection        StartupSelection
	restartCallbacks func(recovery.Transaction) (recovery.RollbackRestartResolver, recovery.RollbackRestartLauncher)
	terminateProcess func(context.Context, recovery.ProcessIdentity) error
}

// Restore 区分无 lease 的 pre-probation 恢复与 current probation lease 回滚，并收敛唯一旧版本启动。
func (service RecoveryRestoreService) Restore(ctx context.Context) (recovery.Transaction, error) {
	if service.restartCallbacks == nil {
		return recovery.Transaction{}, errors.New("Recovery restore restart callbacks are required")
	}
	transaction, err := service.currentTransaction(ctx)
	if err != nil {
		return recovery.Transaction{}, err
	}
	transaction, err = service.stopForeignProbation(ctx, transaction)
	if err != nil {
		return recovery.Transaction{}, err
	}
	transaction, err = service.rollback(ctx, transaction)
	if err != nil {
		return recovery.Transaction{}, err
	}
	resolve, launch := service.restartCallbacks(transaction)
	return service.selection.Store.ConvergeRollbackRestart(ctx, transaction.Identity, resolve, launch)
}

// currentTransaction 按 selector 的完整 identity 等待并加载 current journal，拒绝使用 stale projection。
func (service RecoveryRestoreService) currentTransaction(ctx context.Context) (recovery.Transaction, error) {
	if service.selection.Store == nil || service.selection.Transaction.Identity.TransactionID == "" {
		return recovery.Transaction{}, errors.New("Recovery transaction is unavailable or ambiguous")
	}
	return service.selection.Store.LoadRollbackRestartCurrent(ctx, service.selection.Transaction.Identity)
}

// stopForeignProbation 在任何 journal 或 bundle 变更前认证终止非当前 Recovery 进程持有的 candidate。
func (service RecoveryRestoreService) stopForeignProbation(
	ctx context.Context,
	transaction recovery.Transaction,
) (recovery.Transaction, error) {
	if transaction.State != recovery.StateProbation || !transaction.Probation.LeasePresent ||
		transaction.Probation.Lease.Process == service.selection.process {
		return transaction, nil
	}
	if service.terminateProcess == nil {
		return recovery.Transaction{}, errors.New("Recovery exact probation terminator is required")
	}
	lease := transaction.Probation.Lease
	if err := service.terminateProcess(ctx, lease.Process); err != nil {
		return recovery.Transaction{}, fmt.Errorf("terminate foreign probation process: %w", err)
	}
	current, err := service.currentTransaction(ctx)
	if err != nil {
		return recovery.Transaction{}, err
	}
	if current.State != recovery.StateProbation || !current.Probation.LeasePresent ||
		current.Probation.Lease != lease {
		return recovery.Transaction{}, errors.New("probation lease changed after exact process termination")
	}
	return current, nil
}

// rollback 将 current exact transaction 推进到 rolled_back，已完成状态保持幂等。
func (service RecoveryRestoreService) rollback(
	ctx context.Context,
	transaction recovery.Transaction,
) (recovery.Transaction, error) {
	switch transaction.State {
	case recovery.StatePrepared, recovery.StateBackupRetained, recovery.StateInstallPending:
		return service.selection.Store.Rollback(ctx, transaction.Identity)
	case recovery.StateBackupPending:
		if _, err := service.selection.Store.Replay(ctx, transaction.Identity); err != nil {
			return recovery.Transaction{}, err
		}
		return service.selection.Store.Rollback(ctx, transaction.Identity)
	case recovery.StateProbation:
		if !transaction.Probation.LeasePresent {
			return service.selection.Store.RollbackUnclaimedProbation(ctx, transaction.Identity)
		}
		return service.selection.Store.RollbackClaimed(ctx, transaction.Identity, transaction.Probation.Lease)
	case recovery.StateRollbackPending:
		return service.selection.Store.Replay(ctx, transaction.Identity)
	case recovery.StateRolledBack:
		return transaction, nil
	default:
		return recovery.Transaction{}, fmt.Errorf("Recovery restore is unavailable from state %q", transaction.State)
	}
}

// RecoveryRuntime 是不依赖 desktop/provider/store/toolbridge/skill graph 的阻塞恢复运行时。
type RecoveryRuntime struct {
	State     RecoveryStateService
	Check     RecoveryCheckService
	Retry     RecoveryRetryService
	Restore   RecoveryRestoreService
	selection StartupSelection
	failureMu sync.RWMutex
	failure   RecoveryFailure
}

// RecoverySurface 是 Recovery-only transport/UI 的阻塞生命周期。
type RecoverySurface interface {
	Run(context.Context, *RecoveryRuntime) error
}

// NewRecoveryRuntime 只组装 frozen allowlist 中的四个 Recovery 服务。
func NewRecoveryRuntime(selection StartupSelection) (*RecoveryRuntime, error) {
	if selection.Mode != StartupModeRecovery {
		return nil, errors.New("Recovery runtime requires Recovery startup mode")
	}
	if err := ValidateRecoveryConstructors(RecoveryConstructorIDs()); err != nil {
		return nil, err
	}
	return &RecoveryRuntime{
		State: RecoveryStateService{selection: selection},
		Check: RecoveryCheckService{selection: selection},
		Retry: RecoveryRetryService{selection: selection},
		Restore: RecoveryRestoreService{
			selection: selection, restartCallbacks: recovery.RollbackRestartCallbacks,
			terminateProcess: recovery.TerminateExactProbationProcess,
		},
		selection: selection,
		failure:   selection.Failure,
	}, nil
}

// Run 阻塞运行显式 Recovery surface，不允许退化为无交互 context 等待。
func (runtime *RecoveryRuntime) Run(ctx context.Context, surface RecoverySurface) error {
	if runtime == nil {
		return errors.New("Recovery runtime is required")
	}
	if ctx == nil {
		return errors.New("Recovery runtime context is required")
	}
	if surface == nil {
		return errors.New("Recovery surface is required")
	}
	return surface.Run(ctx, runtime)
}

// CurrentProjection 从 exact journal 刷新 Recovery typed state；无 transaction 时保留 selector 失败原因。
func (runtime *RecoveryRuntime) CurrentProjection(ctx context.Context) (RecoveryProjection, error) {
	if runtime == nil {
		return RecoveryProjection{}, errors.New("Recovery runtime is required")
	}
	if runtime.selection.Transaction.Identity.TransactionID == "" {
		return runtime.selection.Projection, nil
	}
	transaction, err := requireRecoveryTransaction(ctx, runtime.selection)
	if err != nil {
		return RecoveryProjection{}, err
	}
	projection := projectRecoveryTransaction(transaction, runtime.selection.Projection.Reason)
	return projection, nil
}

// CurrentFailure 返回 selector 保存的安全失败元数据，不暴露原始错误。
func (runtime *RecoveryRuntime) CurrentFailure() RecoveryFailure {
	if runtime == nil {
		return RecoveryFailure{}
	}
	runtime.failureMu.RLock()
	failure := runtime.failure
	runtime.failureMu.RUnlock()
	if failure.Code != "" && failure.TransactionID == "" {
		failure.TransactionID = string(runtime.selection.Transaction.Identity.TransactionID)
	}
	return failure
}

// ClearFailure 在显式恢复动作成功后清空 selector failure，避免后续 State 重新暴露旧失败。
func (runtime *RecoveryRuntime) ClearFailure() {
	if runtime == nil {
		return
	}
	runtime.failureMu.Lock()
	runtime.failure = RecoveryFailure{}
	runtime.failureMu.Unlock()
}

func requireRecoveryTransaction(ctx context.Context, selection StartupSelection) (recovery.Transaction, error) {
	if selection.Store == nil || selection.Transaction.Identity.TransactionID == "" {
		return recovery.Transaction{}, errors.New("Recovery transaction is unavailable or ambiguous")
	}
	transaction, err := selection.Store.Load(ctx, selection.Transaction.Identity)
	if err != nil {
		return recovery.Transaction{}, err
	}
	return transaction, nil
}
