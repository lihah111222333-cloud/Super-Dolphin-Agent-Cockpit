//go:build windows

package localci

import (
	"errors"
	"os"
)

var errSchedulerPlatformUnsupported = errors.New("scheduler current-UID exclusive storage is unsupported on this platform")

func currentSchedulerOwnerUID() (int, error) {
	return 0, errSchedulerPlatformUnsupported
}

func schedulerFileOwnerUID(os.FileInfo) (int, error) {
	return 0, errSchedulerPlatformUnsupported
}

func openSchedulerFileNoFollow(string, int, bool) (*os.File, bool, error) {
	return nil, false, errSchedulerPlatformUnsupported
}

func lockSchedulerFile(*os.File) error {
	return errSchedulerPlatformUnsupported
}

func unlockSchedulerFile(*os.File) error {
	return errSchedulerPlatformUnsupported
}

func schedulerLockAlreadyOwned(error) bool {
	return false
}
