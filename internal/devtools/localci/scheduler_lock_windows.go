//go:build windows

package localci

import "os"

func lockSchedulerFile(*os.File) error {
	return errSchedulerPlatformUnsupported
}

func unlockSchedulerFile(*os.File) error {
	return errSchedulerPlatformUnsupported
}

func schedulerLockAlreadyOwned(error) bool {
	return false
}
