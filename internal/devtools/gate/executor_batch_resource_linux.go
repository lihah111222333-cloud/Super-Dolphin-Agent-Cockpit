//go:build linux

package gate

import (
	"errors"
	"os"
	"syscall"
)

// processStatePeakRSSBytes 读取 Linux getrusage 的 KiB 峰值并规范化为字节。
func processStatePeakRSSBytes(state *os.ProcessState) (int64, error) {
	usage, ok := state.SysUsage().(*syscall.Rusage)
	if !ok || usage == nil || usage.Maxrss <= 0 {
		return 0, errors.New("Linux rusage Maxrss is unavailable")
	}
	if usage.Maxrss > int64(^uint64(0)>>1)/1024 {
		return 0, errors.New("Linux rusage Maxrss overflows bytes")
	}
	return usage.Maxrss * 1024, nil
}
