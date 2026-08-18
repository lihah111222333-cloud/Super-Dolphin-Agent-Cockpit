//go:build darwin

package gate

import (
	"errors"
	"os"
	"syscall"
)

// processStatePeakRSSBytes 读取 Darwin getrusage 已以字节报告的峰值。
func processStatePeakRSSBytes(state *os.ProcessState) (int64, error) {
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil || usage.Maxrss <= 0 {
		return 0, errors.New("Darwin rusage Maxrss is unavailable")
	}
	return usage.Maxrss, nil
}
