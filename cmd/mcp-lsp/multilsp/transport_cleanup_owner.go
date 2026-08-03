package multilsp

import (
	"context"
	"errors"
	"fmt"
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

// Error describes the bounded startup cleanup failure and retained owner.
func (e *processTreeCleanupError) Error() string {
	return fmt.Sprintf("LSP process-tree startup cleanup retained owner: %v", e.cause)
}

// Unwrap exposes the operation errors for errors.Is/errors.As callers.
func (e *processTreeCleanupError) Unwrap() error {
	return e.cause
}

// ProcessTreeOwner returns the retained startup owner for a caller that needs
// to retry cleanup after the initial bounded attempt failed.
func (e *processTreeCleanupError) ProcessTreeOwner() processTreeCleanupTarget {
	return e.owner
}

func cleanupProcessTreeOwner(owner processTreeCleanupTarget) error {
	if owner == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
	defer cancel()
	terminateErr := owner.Terminate()
	waitErr := owner.Wait(ctx)
	releaseErr := owner.Release()
	cleanupErr := errors.Join(terminateErr, waitErr, releaseErr)
	if cleanupErr == nil {
		return nil
	}
	return &processTreeCleanupError{owner: owner, cause: cleanupErr}
}
