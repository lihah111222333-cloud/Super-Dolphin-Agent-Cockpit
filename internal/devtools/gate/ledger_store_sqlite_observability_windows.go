//go:build windows

package gate

import "fmt"

// defaultDurationLedgerObservationFilesystemProvider 在 Windows 上拒绝提供 POSIX 文件系统容量事实。
func defaultDurationLedgerObservationFilesystemProvider(string) (durationLedgerObservationFilesystemFacts, error) {
	return durationLedgerObservationFilesystemFacts{}, fmt.Errorf("%w: filesystem capacity is unsupported on windows", errDurationLedgerObservationUnavailable)
}
