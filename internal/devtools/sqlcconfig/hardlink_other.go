//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package sqlcconfig

import (
	"errors"
	"os"
)

func hardLinkCount(_ string, _ os.FileInfo) (uint64, error) {
	return 0, errors.New("output hard-link metadata is unsupported on this platform")
}
