package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrTransactionExists 表示 exact transaction journal 已存在。
	ErrTransactionExists = errors.New("update transaction already exists")
	// ErrTransactionBusy 表示 exact transaction 正由另一个进程持有。
	ErrTransactionBusy = errors.New("update transaction is busy")
)

// Store 持久化并串行化 release 更新事务。
type Store struct {
	root        string
	now         func() time.Time
	afterEffect func(State) error
}

// NewStore 创建持久事务根并同步其父目录。
func NewStore(root string) (*Store, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, fmt.Errorf("update transaction root must be absolute and clean: %q", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create update transaction root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect update transaction root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("update transaction root is not a directory")
	}
	if err := syncDirectory(filepath.Dir(root)); err != nil {
		return nil, err
	}
	return &Store{root: root, now: time.Now}, nil
}

// Create 校验 exact release identity，并原子建立 pending trust 事务。
func (store *Store) Create(ctx context.Context, req CreateRequest) (transaction Transaction, err error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, err
	}
	if err := validateCreateRequest(req); err != nil {
		return Transaction{}, err
	}
	if filepath.Dir(store.root) != filepath.Dir(req.Paths.Target) {
		return Transaction{}, errors.New("transaction journal, target, backup, and staging must share a parent volume")
	}
	if err := persistCreateReleases(req); err != nil {
		return Transaction{}, err
	}
	lock, err := store.acquire(req.Identity.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	defer lock.releaseInto(&err)
	if _, statErr := os.Stat(store.journalPath(req.Identity.TransactionID)); statErr == nil {
		return Transaction{}, ErrTransactionExists
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Transaction{}, fmt.Errorf("inspect update transaction journal: %w", statErr)
	}
	journal := newJournal(req, store.now())
	if err := store.writeLocked(journal); err != nil {
		return Transaction{}, err
	}
	return journal.transaction(), nil
}

// persistCreateReleases 校验 old/candidate identity，并在建 journal 前持久化 staging。
func persistCreateReleases(req CreateRequest) error {
	if err := verifyRelease(req.Paths.Target, req.Identity.OldRelease); err != nil {
		return fmt.Errorf("verify old release: %w", err)
	}
	if err := verifyRelease(req.Paths.Staging, req.Identity.CandidateRelease); err != nil {
		return fmt.Errorf("verify candidate release: %w", err)
	}
	if err := syncRelease(req.Paths.Staging); err != nil {
		return fmt.Errorf("persist candidate release: %w", err)
	}
	return nil
}

// Load 只加载完整 identity 匹配且可重放验证的事务。
func (store *Store) Load(ctx context.Context, identity Identity) (transaction Transaction, err error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, err
	}
	lock, err := store.acquire(identity.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	defer lock.releaseInto(&err)
	journal, err := store.loadExactLocked(identity)
	if err != nil {
		return Transaction{}, err
	}
	return journal.transaction(), nil
}

// RetainBackup 先持久化意图，再用同卷 rename 保留旧 release。
func (store *Store) RetainBackup(ctx context.Context, identity Identity) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		state := journal.transaction().State
		if state == StatePrepared {
			if err := store.advanceLocked(journal, TriggerRetainBackup); err != nil {
				return err
			}
			state = StateBackupPending
		}
		if state == StateBackupRetained {
			return nil
		}
		if state != StateBackupPending {
			return fmt.Errorf("retain backup from state %q: illegal transition", state)
		}
		if err := reconcileBackupEffect(*journal); err != nil {
			return err
		}
		if err := store.runAfterEffect(StateBackupPending); err != nil {
			return err
		}
		return store.advanceLocked(journal, TriggerBackupRetained)
	})
}

// InstallCandidate 先持久化意图，再安装并复验 exact candidate。
func (store *Store) InstallCandidate(ctx context.Context, identity Identity) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		state := journal.transaction().State
		if state == StateBackupRetained {
			if err := store.advanceLocked(journal, TriggerInstallCandidate); err != nil {
				return err
			}
			state = StateInstallPending
		}
		if state == StateProbation {
			return nil
		}
		if state != StateInstallPending {
			return fmt.Errorf("install candidate from state %q: illegal transition", state)
		}
		if err := reconcileInstallEffect(*journal); err != nil {
			return err
		}
		if err := store.runAfterEffect(StateInstallPending); err != nil {
			return err
		}
		return store.advanceLocked(journal, TriggerCandidateInstalled)
	})
}

// CommitHealthy 拒绝没有 supervisor observation 的旧调用。
func (store *Store) CommitHealthy(context.Context, Identity) (Transaction, error) {
	return Transaction{}, ErrHealthyCommitRequiresClaimed
}

// Rollback 只处理 pre-probation exact transaction；probation 必须走显式 lease API。
func (store *Store) Rollback(ctx context.Context, identity Identity) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		if journal.transaction().State == StateProbation {
			if journal.Probation.LeasePresent {
				return ErrProbationLeaseMismatch
			}
			return ErrProbationRollbackRequiresUnclaimed
		}
		return store.rollbackLocked(journal)
	})
}

// commitHealthyLocked 在已持有 transaction lock 时完成 commit 意图和文件效果。
func (store *Store) commitHealthyLocked(journal *journalPayload) error {
	state := journal.transaction().State
	if state == StateCommitted {
		return nil
	}
	if state == StateProbation {
		if !journal.Probation.LeasePresent || !journal.Probation.ACKPresent {
			return errors.New("healthy commit requires current probation lease and exact ACK")
		}
		if err := requirePath(journal.Paths.Backup); err != nil {
			return fmt.Errorf("healthy commit requires retained backup: %w", err)
		}
		if err := store.advanceLocked(journal, TriggerHealthy); err != nil {
			return err
		}
		state = StateCommitPending
	}
	if state != StateCommitPending {
		return fmt.Errorf("healthy commit from state %q: illegal transition", state)
	}
	if err := completeCommitEffect(*journal); err != nil {
		return err
	}
	if err := store.runAfterEffect(StateCommitPending); err != nil {
		return err
	}
	return store.advanceLocked(journal, TriggerCommitCompleted)
}

// rollbackLocked 在已持有 transaction lock 时完成 rollback 意图和文件效果。
func (store *Store) rollbackLocked(journal *journalPayload) error {
	state := journal.transaction().State
	if state == StateRolledBack {
		return nil
	}
	if state != StateRollbackPending {
		if state == StatePrepared {
			if err := validatePreparedRollback(*journal); err != nil {
				return err
			}
		} else if err := requirePath(journal.Paths.Backup); err != nil {
			return fmt.Errorf("rollback requires retained backup: %w", err)
		}
		if err := store.advanceLocked(journal, TriggerRollbackRequested); err != nil {
			return err
		}
	}
	if err := completeRollbackEffect(*journal); err != nil {
		return err
	}
	if err := store.runAfterEffect(StateRollbackPending); err != nil {
		return err
	}
	return store.advanceLocked(journal, TriggerRollbackCompleted)
}

// Replay 只补完 journal 已持久化的文件系统意图，不推断新事务。
func (store *Store) Replay(ctx context.Context, identity Identity) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		switch journal.transaction().State {
		case StateBackupPending:
			if err := reconcileBackupEffect(*journal); err != nil {
				return err
			}
			return store.advanceLocked(journal, TriggerBackupRetained)
		case StateInstallPending:
			if err := reconcileInstallEffect(*journal); err != nil {
				return err
			}
			return store.advanceLocked(journal, TriggerCandidateInstalled)
		case StateCommitPending:
			if err := completeCommitEffect(*journal); err != nil {
				return err
			}
			return store.advanceLocked(journal, TriggerCommitCompleted)
		case StateRollbackPending:
			if err := completeRollbackEffect(*journal); err != nil {
				return err
			}
			return store.advanceLocked(journal, TriggerRollbackCompleted)
		default:
			return nil
		}
	})
}

func (store *Store) advance(ctx context.Context, identity Identity, trigger Trigger) (Transaction, error) {
	return store.withExact(ctx, identity, func(journal *journalPayload) error {
		return store.advanceLocked(journal, trigger)
	})
}

func (store *Store) withExact(ctx context.Context, identity Identity, action func(*journalPayload) error) (transaction Transaction, err error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, err
	}
	lock, err := store.acquire(identity.TransactionID)
	if err != nil {
		return Transaction{}, err
	}
	defer lock.releaseInto(&err)
	journal, err := store.loadExactLocked(identity)
	if err != nil {
		return Transaction{}, err
	}
	if err := action(&journal); err != nil {
		return Transaction{}, err
	}
	return journal.transaction(), nil
}

func (store *Store) advanceLocked(journal *journalPayload, trigger Trigger) error {
	current := journal.transaction().State
	next, err := nextState(current, trigger)
	if err != nil {
		return err
	}
	timestamp := store.now().UTC().Format(time.RFC3339Nano)
	journal.Entries = append(journal.Entries, journalEntry{
		Sequence: uint64(len(journal.Entries) + 1),
		Trigger:  trigger,
		State:    next,
		At:       timestamp,
	})
	journal.Trust.State = trustStateFor(next)
	journal.UpdatedAt = timestamp
	return store.writeLocked(*journal)
}

func (store *Store) loadExactLocked(identity Identity) (journalPayload, error) {
	if err := validateIdentity(identity); err != nil {
		return journalPayload{}, err
	}
	raw, err := os.ReadFile(store.journalPath(identity.TransactionID))
	if err != nil {
		return journalPayload{}, fmt.Errorf("read update transaction journal: %w", err)
	}
	journal, err := decodeJournal(raw)
	if err != nil {
		return journalPayload{}, err
	}
	if journal.Identity != identity {
		return journalPayload{}, ErrIdentityMismatch
	}
	return journal, nil
}

func (store *Store) writeLocked(journal journalPayload) error {
	raw, err := encodeJournal(journal)
	if err != nil {
		return err
	}
	return atomicWrite(store.journalPath(journal.Identity.TransactionID), raw)
}

func (store *Store) journalPath(id TransactionID) string {
	return filepath.Join(store.root, string(id), "journal.json")
}

func (store *Store) transactionDir(id TransactionID) string {
	return filepath.Join(store.root, string(id))
}

func (store *Store) runAfterEffect(state State) error {
	if store.afterEffect == nil {
		return nil
	}
	return store.afterEffect(state)
}

func requireContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("update transaction context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
