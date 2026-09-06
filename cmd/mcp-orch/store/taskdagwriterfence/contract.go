// Package taskdagwriterfence is an isolated B10 contract candidate.
//
// It is deliberately transport-neutral and is not wired into taskdag, Fx, or
// the shared application database.  The package is a small executable contract
// for a future mutation owner: every write transaction must first perform a
// DB-resident compare-and-swap against its writer epoch and deployment fence.
package taskdagwriterfence

import (
	"errors"
	"fmt"
	"strings"
)

const WriterFenceSchemaVersion int64 = 1

var (
	ErrInvalidAdmission = errors.New("taskdag writer admission is invalid")
	ErrStaleWriter      = errors.New("taskdag writer is stale")
	ErrAlreadyFenced    = errors.New("taskdag writer is fenced")
	ErrCommitUnknown    = errors.New("taskdag writer commit outcome is unknown")
)

// WriterFence is the immutable token captured by one writer deployment.
type WriterFence struct {
	SchemaVersion   int64
	WriterEpoch     int64
	DeploymentFence string
}

// Validate 拒绝隐式默认值、版本漂移和格式错误的 admission token。
func (f WriterFence) Validate() error {
	if f.SchemaVersion != WriterFenceSchemaVersion || f.WriterEpoch <= 0 {
		return fmt.Errorf("%w: schema version or epoch", ErrInvalidAdmission)
	}
	if strings.TrimSpace(f.DeploymentFence) != f.DeploymentFence || f.DeploymentFence == "" || len(f.DeploymentFence) > 512 || strings.IndexByte(f.DeploymentFence, 0) >= 0 {
		return fmt.Errorf("%w: deployment fence", ErrInvalidAdmission)
	}
	return nil
}

// Admission is the explicit prerequisite for opening a mutation owner.
// Missing values are errors; no local default can authorize a writer.
type Admission struct {
	Fence WriterFence
}
