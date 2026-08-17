//go:build !darwin && !linux && !windows

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
)

var errUnsupportedProcessTree = errors.New("process-tree is unsupported on this platform")
var ErrProcessTreeCleanupPending = errors.New("CleanupPending: process-tree destructive action is blocked")

type otherProcessTree struct{}

func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	_ = cmd
	return nil, errUnsupportedProcessTree
}

func (p *otherProcessTree) terminate() error { return errUnsupportedProcessTree }
func (p *otherProcessTree) release() error   { return errUnsupportedProcessTree }
func (p *otherProcessTree) rssBytes() (uint64, error) {
	return 0, errUnsupportedProcessTree
}
func (p *otherProcessTree) identity() (ProcessIdentity, error) {
	return ProcessIdentity{}, errUnsupportedProcessTree
}
func (p *otherProcessTree) snapshot() (ProcessTreeSnapshot, error) {
	return ProcessTreeSnapshot{}, errUnsupportedProcessTree
}
func (p *otherProcessTree) prepareShutdown() error { return errUnsupportedProcessTree }
func (p *otherProcessTree) alive() (bool, error)   { return false, errUnsupportedProcessTree }
func (p *otherProcessTree) descendants() ([]ProcessIdentity, error) {
	return nil, errUnsupportedProcessTree
}
func (p *otherProcessTree) graceful(context.Context) error { return errUnsupportedProcessTree }
func (p *otherProcessTree) force(context.Context) error    { return errUnsupportedProcessTree }
func (p *otherProcessTree) wait(context.Context) error     { return errUnsupportedProcessTree }
func (p *otherProcessTree) remaining() ([]ProcessIdentity, error) {
	return nil, errUnsupportedProcessTree
}

func processTreeRSSBytes(int) (uint64, error) { return 0, errUnsupportedProcessTree }
func processRSSBytes(int) (uint64, error)     { return 0, errUnsupportedProcessTree }
func processAlive(int) (bool, error)          { return false, errUnsupportedProcessTree }
func processStartIdentity(int) (string, error) {
	return "", errUnsupportedProcessTree
}
func killProcessTree(*exec.Cmd) error { return errUnsupportedProcessTree }

func terminateStartupProcess(*exec.Cmd) error {
	return errors.Join(ErrProcessTreeCleanupPending, errors.New("startup exact-root signal authority is unavailable; signal_sent=false"))
}
