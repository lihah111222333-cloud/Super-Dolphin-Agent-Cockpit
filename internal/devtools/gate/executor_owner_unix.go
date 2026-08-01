//go:build !windows

package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func fileOwnerUID(info fs.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}

func lockDurationLedgerFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("%w: lock duration ledger: %w", ErrDurationLedgerBusy, err)
		}
		return fmt.Errorf("lock duration ledger: %w", err)
	}
	return nil
}

func unlockDurationLedgerFile(file *os.File) error {
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock duration ledger: %w", err)
	}
	return nil
}

func syncDurationLedgerDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open duration ledger directory: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return errors.Join(fmt.Errorf("sync duration ledger directory: %w", syncErr), closeErr)
	}
	return closeErr
}
