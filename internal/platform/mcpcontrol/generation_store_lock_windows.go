//go:build windows

package mcpcontrol

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func withGenerationOwnerLock(path string, fn func() error) error {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open mcpcontrol generation owner lock: %w", err)
	}
	defer file.Close()
	handle := windows.Handle(file.Fd())
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		return fmt.Errorf("lock mcpcontrol generation owner: %w", err)
	}
	runErr := fn()
	unlockErr := windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
	if unlockErr != nil {
		unlockErr = fmt.Errorf("unlock mcpcontrol generation owner: %w", unlockErr)
	}
	return errors.Join(runErr, unlockErr)
}
