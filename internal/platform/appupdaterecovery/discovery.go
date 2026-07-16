package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const abortedTransactionPrefix = ".aborted-"

var ErrMultipleActiveTransactions = errors.New("multiple active update transactions")

// LoadByID 从 journal 自身恢复 exact identity，并校验目录 ID 一致。
func (store *Store) LoadByID(ctx context.Context, id TransactionID) (transaction Transaction, err error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, err
	}
	lock, err := store.acquire(id)
	if err != nil {
		return Transaction{}, err
	}
	defer lock.releaseInto(&err)
	raw, err := os.ReadFile(store.journalPath(id))
	if err != nil {
		return Transaction{}, fmt.Errorf("read update transaction journal: %w", err)
	}
	journal, err := decodeJournal(raw)
	if err != nil {
		return Transaction{}, err
	}
	if journal.Identity.TransactionID != id {
		return Transaction{}, ErrIdentityMismatch
	}
	return journal.transaction(), nil
}

// SelectForTarget 返回 exact target 的唯一 active transaction，或最新终态事务。
func (store *Store) SelectForTarget(ctx context.Context, target string) (Transaction, bool, error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, false, err
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return Transaction{}, false, fmt.Errorf("read update transaction root: %w", err)
	}
	selection := targetTransactionSelection{target: target}
	for _, entry := range entries {
		transaction, found, err := store.loadTransactionEntry(ctx, entry)
		if err != nil {
			return Transaction{}, false, err
		}
		if !found {
			continue
		}
		if err := selection.add(transaction); err != nil {
			return Transaction{}, false, err
		}
	}
	if selection.active.Identity.TransactionID != "" {
		return selection.active, true, nil
	}
	return selection.latest, selection.latest.Identity.TransactionID != "", nil
}

type targetTransactionSelection struct {
	target   string
	active   Transaction
	latest   Transaction
	latestAt time.Time
}

// add 将 exact target 的 active 或最新终态事务加入选择器。
func (selection *targetTransactionSelection) add(transaction Transaction) error {
	if transaction.Paths.Target != selection.target {
		return nil
	}
	if transaction.State != StateCommitted && transaction.State != StateRolledBack {
		if selection.active.Identity.TransactionID != "" {
			return ErrMultipleActiveTransactions
		}
		selection.active = transaction
		return nil
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, transaction.UpdatedAt)
	if err != nil {
		return fmt.Errorf("parse transaction update time: %w", err)
	}
	if selection.latest.Identity.TransactionID == "" || updatedAt.After(selection.latestAt) {
		selection.latest = transaction
		selection.latestAt = updatedAt
	}
	return nil
}

// loadTransactionEntry 只加载 exact journal 目录，并显式治理已知崩溃半成品。
func (store *Store) loadTransactionEntry(ctx context.Context, entry os.DirEntry) (Transaction, bool, error) {
	if !entry.IsDir() {
		return Transaction{}, false, fmt.Errorf("unexpected update transaction root entry %q", entry.Name())
	}
	if strings.HasPrefix(entry.Name(), abortedTransactionPrefix) {
		if err := store.cleanAbortedTransactionEntry(entry.Name()); err != nil {
			return Transaction{}, false, err
		}
		return Transaction{}, false, nil
	}
	id := TransactionID(entry.Name())
	if err := validateTransactionID(id); err != nil {
		return Transaction{}, false, err
	}
	cleaned, err := store.cleanIncompleteTransaction(id)
	if err != nil {
		return Transaction{}, false, err
	}
	if cleaned {
		return Transaction{}, false, nil
	}
	transaction, err := store.LoadByID(ctx, id)
	if err != nil {
		return Transaction{}, false, fmt.Errorf("load update transaction %s: %w", id, err)
	}
	return transaction, true, nil
}

// cleanAbortedTransactionEntry 只治理 exact 内部 tombstone；伪造名称继续阻断扫描。
func (store *Store) cleanAbortedTransactionEntry(name string) error {
	id := TransactionID(strings.TrimPrefix(name, abortedTransactionPrefix))
	if err := validateTransactionID(id); err != nil {
		return fmt.Errorf("invalid aborted transaction entry %q: %w", name, err)
	}
	if err := os.RemoveAll(filepath.Join(store.root, name)); err != nil {
		return fmt.Errorf("remove aborted transaction tombstone: %w", err)
	}
	return syncDirectory(store.root)
}

// cleanIncompleteTransaction 将无 journal 的已知半成品原子终止；未知内容仍阻断扫描。
func (store *Store) cleanIncompleteTransaction(id TransactionID) (cleaned bool, err error) {
	missing, err := store.journalMissing(id, "inspect")
	if err != nil || !missing {
		return false, err
	}
	lock, err := store.acquire(id)
	if err != nil {
		return false, err
	}
	defer lock.releaseInto(&err)
	missing, err = store.journalMissing(id, "reinspect")
	if err != nil || !missing {
		return false, err
	}
	if err := store.cleanIncompleteTransactionContents(id); err != nil {
		return false, err
	}
	lock.releaseInto(&err)
	if err != nil {
		return false, err
	}
	if err := store.abortIncompleteTransaction(id); err != nil {
		return false, err
	}
	return true, nil
}

func (store *Store) journalMissing(id TransactionID, action string) (bool, error) {
	_, err := os.Stat(store.journalPath(id))
	if err == nil {
		return false, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return false, fmt.Errorf("%s incomplete transaction journal: %w", action, err)
}

// cleanIncompleteTransactionContents 仅清理 scanner 明确认识的 lock 与 capsule 内容。
func (store *Store) cleanIncompleteTransactionContents(id TransactionID) error {
	dir := store.transactionDir(id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read incomplete transaction directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == "transaction.lock" {
			continue
		}
		if entry.Name() != "recovery" && entry.Name() != "recovery.pending" {
			return fmt.Errorf("unexpected incomplete transaction entry %q", entry.Name())
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return fmt.Errorf("remove incomplete transaction capsule: %w", err)
		}
	}
	return syncDirectory(dir)
}

func (store *Store) abortIncompleteTransaction(id TransactionID) error {
	dir := store.transactionDir(id)
	tombstone := filepath.Join(store.root, abortedTransactionPrefix+string(id))
	if err := os.Rename(dir, tombstone); err != nil {
		return fmt.Errorf("publish aborted transaction tombstone: %w", err)
	}
	if err := syncDirectory(store.root); err != nil {
		return err
	}
	if err := os.RemoveAll(tombstone); err != nil {
		return fmt.Errorf("remove aborted transaction tombstone: %w", err)
	}
	if err := syncDirectory(store.root); err != nil {
		return err
	}
	return store.cleanOrphanStaging(id)
}

func (store *Store) cleanOrphanStaging(id TransactionID) error {
	parent := filepath.Dir(store.root)
	matches, err := filepath.Glob(filepath.Join(parent, ".*.staging-"+string(id)+".app"))
	if err != nil {
		return fmt.Errorf("find orphan transaction staging: %w", err)
	}
	if len(matches) > 1 {
		return fmt.Errorf("multiple orphan staging artifacts for transaction %s", id)
	}
	if len(matches) == 1 {
		if err := os.RemoveAll(matches[0]); err != nil {
			return fmt.Errorf("remove orphan transaction staging: %w", err)
		}
		return syncDirectory(parent)
	}
	return nil
}

// SelectActive 严格扫描 root；损坏、未知条目或多个 active transaction 都 fail-fast。
func (store *Store) SelectActive(ctx context.Context) (Transaction, bool, error) {
	if err := requireContext(ctx); err != nil {
		return Transaction{}, false, err
	}
	entries, err := os.ReadDir(store.root)
	if err != nil {
		return Transaction{}, false, fmt.Errorf("read update transaction root: %w", err)
	}
	var active Transaction
	found := false
	for _, entry := range entries {
		transaction, loaded, err := store.loadTransactionEntry(ctx, entry)
		if err != nil {
			return Transaction{}, false, err
		}
		if !loaded {
			continue
		}
		if transaction.State == StateCommitted || transaction.State == StateRolledBack {
			continue
		}
		if found {
			return Transaction{}, false, ErrMultipleActiveTransactions
		}
		active = transaction
		found = true
	}
	return active, found, nil
}
