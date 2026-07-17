package app

import (
	"context"
	"errors"
	"fmt"
	"slices"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

var allowedRecoveryConstructors = []string{
	"recovery.state",
	"recovery.check",
	"recovery.retry",
	"recovery.restore",
}

// RecoveryConstructorIDs 返回 frozen Recovery graph 的精确构造器顺序。
func RecoveryConstructorIDs() []string {
	return slices.Clone(allowedRecoveryConstructors)
}

// ValidateRecoveryConstructors 拒绝新增、缺失、重复或乱序的 Recovery 构造器。
func ValidateRecoveryConstructors(constructors []string) error {
	if !slices.Equal(constructors, allowedRecoveryConstructors) {
		return fmt.Errorf("Recovery constructors %v do not match frozen allowlist %v", constructors, allowedRecoveryConstructors)
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
		return errors.New("Recovery check found candidate digest mismatch")
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
	selection StartupSelection
}

// Restore 区分无 lease 的 pre-probation 恢复与 current probation lease 回滚。
func (service RecoveryRestoreService) Restore(ctx context.Context) (recovery.Transaction, error) {
	transaction, err := requireRecoveryTransaction(ctx, service.selection)
	if err != nil {
		return recovery.Transaction{}, err
	}
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
		State:     RecoveryStateService{selection: selection},
		Check:     RecoveryCheckService{selection: selection},
		Retry:     RecoveryRetryService{selection: selection},
		Restore:   RecoveryRestoreService{selection: selection},
		selection: selection,
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
	return projectRecoveryTransaction(transaction, runtime.selection.Projection.Reason), nil
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
