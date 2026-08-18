//go:build !linux && !darwin

package gate

import (
	"errors"
	"os"
)

// processStatePeakRSSBytes 在非远程目标平台显式拒绝伪造 RSS 证据。
func processStatePeakRSSBytes(_ *os.ProcessState) (int64, error) {
	return 0, errors.New("batch resource RSS observation is unsupported on this platform")
}
