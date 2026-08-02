//go:build !(aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris || windows)

package remoteci

import (
	"errors"
	"os"
)

func sourceSnapshotFileHasSingleLink(_ string, _ os.FileInfo) (bool, error) {
	return false, errors.New("source snapshot hard-link validation is unsupported on this platform")
}
