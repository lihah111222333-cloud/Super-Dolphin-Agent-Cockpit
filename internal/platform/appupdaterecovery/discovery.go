package appupdaterecovery

import (
	"context"
	"errors"
	"fmt"
	"os"
)

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
		if !entry.IsDir() {
			return Transaction{}, false, fmt.Errorf("unexpected update transaction root entry %q", entry.Name())
		}
		id := TransactionID(entry.Name())
		if err := validateTransactionID(id); err != nil {
			return Transaction{}, false, err
		}
		transaction, err := store.LoadByID(ctx, id)
		if err != nil {
			return Transaction{}, false, fmt.Errorf("load update transaction %s: %w", id, err)
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
