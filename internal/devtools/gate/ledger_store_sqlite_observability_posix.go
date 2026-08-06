//go:build darwin || linux || freebsd

package gate

import (
	"errors"
	"fmt"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// defaultDurationLedgerObservationFilesystemProvider 从 authority 文件和父文件系统读取容量事实。
func defaultDurationLedgerObservationFilesystemProvider(path string) (durationLedgerObservationFilesystemFacts, error) {
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		return durationLedgerObservationFilesystemFacts{}, fmt.Errorf("%w: stat ledger authority: %v", errDurationLedgerObservationUnavailable, err)
	}
	physical, err := durationLedgerObservationSignedCapacityBytes("duration ledger physical bytes", stat.Blocks, 512)
	if err != nil {
		return durationLedgerObservationFilesystemFacts{}, fmt.Errorf("duration ledger physical bytes provider returned malformed block count: %w", err)
	}
	facts := durationLedgerObservationFilesystemFacts{PhysicalBytes: &physical}
	var filesystem unix.Statfs_t
	if err := unix.Statfs(filepath.Dir(path), &filesystem); err != nil {
		return facts, fmt.Errorf("%w: stat filesystem capacity: %v", errDurationLedgerObservationUnavailable, err)
	}
	if filesystem.Bsize <= 0 {
		return facts, errors.New("filesystem available bytes provider returned malformed block unit")
	}
	available, err := durationLedgerObservationUnsignedCapacityBytes(
		"filesystem available bytes",
		uint64(filesystem.Bavail),
		uint64(filesystem.Bsize),
	)
	if err != nil {
		return facts, fmt.Errorf("filesystem available bytes provider returned malformed capacity: %w", err)
	}
	facts.AvailableBytes = &available
	return facts, nil
}
