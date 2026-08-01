//go:build !darwin && !linux && !windows

package hiddenexec

import (
	"errors"
	"os"
	"os/exec"
)

func configureCommand(cmd *exec.Cmd) {
	_ = cmd
}

type otherProcessTree struct {
	cmd *exec.Cmd
}

func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	if cmd == nil {
		return nil, errors.New("start process tree: command is nil")
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &ProcessTree{controller: &otherProcessTree{cmd: cmd}}, nil
}

func (p *otherProcessTree) terminate() error {
	return killProcessTree(p.cmd)
}

func (p *otherProcessTree) release() error {
	return nil
}

func (p *otherProcessTree) rssBytes() (uint64, error) {
	return 0, errors.New("process-tree RSS is unsupported on this platform")
}

func processTreeRSSBytes(int) (uint64, error) {
	return 0, errors.New("process-tree RSS is unsupported on this platform")
}

func processRSSBytes(int) (uint64, error) {
	return 0, errors.New("process RSS is unsupported on this platform")
}

func processAlive(int) (bool, error) {
	return false, errors.New("process liveness probe is unsupported on this platform")
}

func processStartIdentity(int) (string, error) {
	return "", errors.New("process start identity is unsupported on this platform")
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}
