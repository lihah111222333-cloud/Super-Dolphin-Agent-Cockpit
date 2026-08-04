package multilsp

import (
	"context"
	"errors"
	"fmt"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// processTreeCleanupTarget is the owner handoff boundary for startup failures.
// A failed synchronous cleanup keeps this same target inside the returned error
// so the caller can retry without reconstructing a process identity.
type processTreeCleanupTarget interface {
	Terminate() error
	Wait(context.Context) error
	Release() error
}

type processTreeCleanupError struct {
	owner processTreeCleanupTarget
	cause error
}

// Error 返回有界启动清理失败及保留 owner 的描述。
func (e *processTreeCleanupError) Error() string {
	return fmt.Sprintf("LSP process-tree startup cleanup retained owner: %v", e.cause)
}

// Unwrap 暴露底层操作错误，供 errors.Is/errors.As 调用方识别。
func (e *processTreeCleanupError) Unwrap() error {
	return e.cause
}

// ProcessTreeOwner 返回保留的启动 owner，供有界清理失败后的重试调用方使用。
func (e *processTreeCleanupError) ProcessTreeOwner() processTreeCleanupTarget {
	return e.owner
}

func cleanupProcessTreeOwner(owner processTreeCleanupTarget) error {
	if owner == nil {
		return nil
	}
	ctx, cancel := platformconfig.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	terminateErr := owner.Terminate()
	waitErr := owner.Wait(ctx)
	var releaseErr error
	if terminateErr == nil && waitErr == nil {
		releaseErr = owner.Release()
	}
	cleanupErr := errors.Join(terminateErr, waitErr, releaseErr)
	if cleanupErr == nil {
		return nil
	}
	return &processTreeCleanupError{owner: owner, cause: cleanupErr}
}
