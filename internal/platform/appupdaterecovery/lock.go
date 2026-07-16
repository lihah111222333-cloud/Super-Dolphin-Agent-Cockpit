package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type transactionLock struct {
	file *os.File
}

// acquire 获取单事务非阻塞跨进程锁，并同步新建事务目录。
func (store *Store) acquire(id TransactionID) (*transactionLock, error) {
	if err := validateTransactionID(id); err != nil {
		return nil, err
	}
	dir := store.transactionDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create update transaction directory: %w", err)
	}
	if err := syncDirectory(store.root); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "transaction.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update transaction lock: %w", err)
	}
	if err := tryAcquireTransactionFileLock(file); err != nil {
		closeErr := file.Close()
		if isTransactionFileLockBusy(err) {
			return nil, errors.Join(ErrTransactionBusy, closeErr)
		}
		return nil, errors.Join(fmt.Errorf("acquire update transaction lock: %w", err), closeErr)
	}
	return &transactionLock{file: file}, nil
}

func (lock *transactionLock) releaseInto(target *error) {
	if lock == nil || lock.file == nil {
		return
	}
	file := lock.file
	lock.file = nil
	unlockErr := releaseTransactionFileLock(file)
	closeErr := file.Close()
	*target = errors.Join(*target, unlockErr, closeErr)
}
