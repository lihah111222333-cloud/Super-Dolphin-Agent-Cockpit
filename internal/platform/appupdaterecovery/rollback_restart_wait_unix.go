//go:build darwin || linux || freebsd

package appupdaterecovery

import (
	"context"
	"errors"
	"syscall"
	"time"
)

const rollbackRestartChildWaitPoll = 10 * time.Millisecond

// waitRollbackRestartChild 以 WNOHANG 轮询回收 direct child，避免无界 Wait。
func waitRollbackRestartChild(ctx context.Context, childPID int) error {
	ticker := time.NewTicker(rollbackRestartChildWaitPoll)
	defer ticker.Stop()
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(childPID, &status, syscall.WNOHANG, nil)
		if pid == childPID {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-ticker.C:
		}
	}
}
