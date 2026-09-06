package sqliteauthority

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const kernelAuthorityLeaseSentinel = ".recovery-custody-authority.lock"

// KernelAuthorityLease 用内核目录锁和固定路径身份线性化一个 authority writer。
type KernelAuthorityLease struct {
	mu sync.Mutex

	scope             AuthorityScope
	authorityPath     string
	authorityIdentity authorityPathIdentity
	lock              *securefs.PinnedDirectoryLock
	closed            bool
}

// AcquireKernelAuthorityLease 在 authority 数据库的物理父目录上取得唯一内核租约。
func AcquireKernelAuthorityLease(ctx context.Context, config Config) (*KernelAuthorityLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	physicalAuthorityPath, err := validateIndependentPaths(config)
	if err != nil {
		return nil, err
	}
	authorityIdentity, err := captureAuthorityPathIdentity(physicalAuthorityPath)
	if err != nil {
		return nil, err
	}
	scope, err := authorityScopeFromConfig(config, physicalAuthorityPath)
	if err != nil {
		return nil, err
	}
	lock, err := securefs.AcquirePinnedDirectoryLock(filepath.Dir(physicalAuthorityPath), kernelAuthorityLeaseSentinel)
	if err != nil {
		return nil, fmt.Errorf("%w: kernel authority lock unavailable", ErrLeaseRejected)
	}
	lease := &KernelAuthorityLease{scope: scope, authorityPath: physicalAuthorityPath,
		authorityIdentity: authorityIdentity, lock: lock}
	if err := lease.Validate(ctx, scope); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lease, nil
}

// Validate 在线性化点重新校验 scope、目录权限、目录身份和固定哨兵身份。
func (l *KernelAuthorityLease) Validate(ctx context.Context, scope AuthorityScope) error {
	if l == nil {
		return ErrLeaseRejected
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.validateLocked(ctx, scope)
}

// WithValidatedScope 在完整 callback 前后重验固定目录锁，并与 Validate、Close 线性化。
func (l *KernelAuthorityLease) WithValidatedScope(
	ctx context.Context,
	scope AuthorityScope,
	callback func() error,
) error {
	if l == nil {
		return ErrLeaseRejected
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if callback == nil {
		return ErrLeaseRejected
	}
	if err := l.validateLocked(ctx, scope); err != nil {
		return err
	}
	callbackErr := callback()
	if err := l.validateLockIdentityLocked(); err != nil {
		return errors.Join(callbackErr, ErrLeaseRejected, ErrCommitOutcomeUnknown)
	}
	observed, err := observeAuthorityPathIdentity(l.authorityPath, l.authorityIdentity, true)
	if err != nil {
		return errors.Join(callbackErr, err, ErrCommitOutcomeUnknown)
	}
	l.authorityIdentity = observed
	return callbackErr
}

// withValidatedScopeAndIdentity 在同一内核锁内完成启动 open/bootstrap/takeover
// 及 DB 身份回读；普通 mutation 仍使用 WithValidatedScope，不能静默重绑已打开实例。
func (l *KernelAuthorityLease) withValidatedScopeAndIdentity(
	ctx context.Context,
	scope AuthorityScope,
	path string,
	callback func() error,
) error {
	if l == nil {
		return ErrLeaseRejected
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	expected, err := l.validateStartupIdentityLocked(ctx, scope, path, callback)
	if err != nil {
		return err
	}
	callbackErr := callback()
	return l.finishStartupIdentityLocked(path, expected, callbackErr)
}

// validateStartupIdentityLocked 在启动 callback 前检查 lease scope 与文件对象；
// fresh Bootstrap 仍强制要求主库保持缺失，避免外来文件占据创建路径。
func (l *KernelAuthorityLease) validateStartupIdentityLocked(
	ctx context.Context,
	scope AuthorityScope,
	path string,
	callback func() error,
) (authorityPathIdentity, error) {
	if callback == nil || path == "" || path != l.authorityPath {
		return authorityPathIdentity{}, ErrLeaseRejected
	}
	expected := l.authorityIdentity
	if err := l.validateLocked(ctx, scope); err != nil {
		return authorityPathIdentity{}, err
	}
	if err := validateAuthorityPathIdentity(path, expected, false); err != nil {
		return authorityPathIdentity{}, err
	}
	return expected, nil
}

// finishStartupIdentityLocked 在启动 callback 后复核锁与 DB 对象，并在成功时
// 将 SQLite 主库及 sidecar 的最新快照绑定回 lease；调用方须持有 l.mu。
func (l *KernelAuthorityLease) finishStartupIdentityLocked(
	path string,
	expected authorityPathIdentity,
	callbackErr error,
) error {
	if err := l.validateLockIdentityLocked(); err != nil {
		return errors.Join(callbackErr, ErrLeaseRejected, ErrCommitOutcomeUnknown)
	}
	observed, identityErr := observeAuthorityPathIdentity(path, expected, true)
	if identityErr != nil {
		return errors.Join(callbackErr, identityErr, ErrCommitOutcomeUnknown)
	}
	if callbackErr != nil {
		return callbackErr
	}
	if !observed.main.present {
		return errors.Join(ErrLeaseRejected, ErrCommitOutcomeUnknown)
	}
	l.authorityIdentity = observed
	return nil
}

// Close 等待正在租约临界区内的事务结束，再幂等释放内核锁和固定句柄。
func (l *KernelAuthorityLease) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if err := l.lock.Close(); err != nil {
		return fmt.Errorf("close kernel authority lease: %w", err)
	}
	return nil
}

// validateLocked 校验上下文、生命周期、完整 scope 与固定目录锁身份。
func (l *KernelAuthorityLease) validateLocked(ctx context.Context, scope AuthorityScope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if l.closed || scope != l.scope {
		return ErrLeaseRejected
	}
	if err := l.validateLockIdentityLocked(); err != nil {
		return err
	}
	// A fresh Bootstrap starts with no main DB file.  The startup wrapper does
	// a strict pre-open check and binds the newly-created identity before the
	// lock is released; regular callers must retain every existing inode.
	observed, err := observeAuthorityPathIdentity(l.authorityPath, l.authorityIdentity, true)
	if err != nil {
		return err
	}
	l.authorityIdentity = observed
	return nil
}

// validateLockIdentityLocked 把 securefs 身份错误收敛成稳定租约拒绝分类。
func (l *KernelAuthorityLease) validateLockIdentityLocked() error {
	if l.lock == nil {
		return ErrLeaseRejected
	}
	if err := l.lock.Validate(); err != nil {
		return ErrLeaseRejected
	}
	return nil
}

// authorityIdentitySnapshot returns the immutable startup snapshot associated
// with this lease.  It deliberately does not recanonicalize a caller path.
func (l *KernelAuthorityLease) authorityIdentitySnapshot(path string) (authorityPathIdentity, error) {
	if l == nil {
		return authorityPathIdentity{}, ErrLeaseRejected
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || path != l.authorityPath {
		return authorityPathIdentity{}, ErrLeaseRejected
	}
	return l.authorityIdentity, nil
}

var _ AuthorityLease = (*KernelAuthorityLease)(nil)
