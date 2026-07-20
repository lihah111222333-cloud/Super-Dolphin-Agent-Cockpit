//go:build !linux && !windows

package gate

import (
	"errors"
	"fmt"
	"syscall"
)

func commandProcessGroupGone(processGroupID int) (bool, error) {
	err := syscall.Kill(-processGroupID, 0)
	if errors.Is(err, syscall.ESRCH) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect executor process group %d after termination: %w", processGroupID, err)
	}
	return false, nil
}
