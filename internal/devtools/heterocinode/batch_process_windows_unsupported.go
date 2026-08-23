//go:build windows

package heterocinode

import (
	"errors"
	"os/exec"
)

var errUnsupportedPhysicalWindows = errors.New("heteroci physical Windows execution is unsupported")

type batchProcessGuard struct{}

func startBatchProcess(*exec.Cmd) (batchProcessGuard, error) {
	return batchProcessGuard{}, errUnsupportedPhysicalWindows
}

func (batchProcessGuard) stop(*exec.Cmd) error {
	return errUnsupportedPhysicalWindows
}

func (batchProcessGuard) close() error {
	return errUnsupportedPhysicalWindows
}

func isBatchProcessGone(error) bool {
	return false
}
