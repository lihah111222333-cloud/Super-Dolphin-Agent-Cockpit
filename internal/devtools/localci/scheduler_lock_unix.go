//go:build unix

package localci

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockSchedulerFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func unlockSchedulerFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func schedulerLockAlreadyOwned(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
