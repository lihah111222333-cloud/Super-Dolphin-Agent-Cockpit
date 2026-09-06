//go:build darwin || linux

package standalone

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryReadyLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockReady(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func readyLockBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
