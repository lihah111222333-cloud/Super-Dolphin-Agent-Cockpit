//go:build !windows

package mcpcontrol

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func withGenerationOwnerLock(path string, fn func() error) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open mcpcontrol generation owner lock: %w", err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock mcpcontrol generation owner: %w", err)
	}
	runErr := fn()
	unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	if unlockErr != nil {
		unlockErr = fmt.Errorf("unlock mcpcontrol generation owner: %w", unlockErr)
	}
	return errors.Join(runErr, unlockErr)
}
