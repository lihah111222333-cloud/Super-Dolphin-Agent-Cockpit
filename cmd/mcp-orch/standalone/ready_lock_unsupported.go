//go:build !darwin && !linux && !windows

package standalone

import (
	"errors"
	"os"
)

func tryReadyLock(*os.File) error {
	return errors.New("ready inode locking is unsupported on this platform")
}
func unlockReady(*os.File) error { return nil }
func readyLockBusy(error) bool   { return false }
