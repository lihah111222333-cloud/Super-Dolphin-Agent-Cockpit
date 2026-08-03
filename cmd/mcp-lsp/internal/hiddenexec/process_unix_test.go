//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestConfigureCommandCreatesIndependentSessionAndProcessGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh")
	configureCommand(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatal("language-server command does not create an independent session")
	}
}

func TestCommandContextCancellationRefusesUnownedDestructiveAction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := CommandContext(
		ctx,
		"/bin/sh",
		"-c",
		"sleep 0.2",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start CommandContext helper: %v", err)
	}
	if err := cmd.Cancel(); !errors.Is(err, ErrProcessTreeOwnerMissing) {
		t.Fatalf("CommandContext Cancel() error = %v, want owner-missing", err)
	}
	cancel()
	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()
	select {
	case <-waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("unowned CommandContext process did not exit naturally")
	}
	parentAlive, err := ProcessAlive(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("probe CommandContext parent after Wait: %v", err)
	}
	if parentAlive {
		t.Fatal("CommandContext parent is still alive after Wait")
	}
}
