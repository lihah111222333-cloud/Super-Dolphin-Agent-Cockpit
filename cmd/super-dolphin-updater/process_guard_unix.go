//go:build !windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const guardProcessTreeGracePeriod = 500 * time.Millisecond

type guardProcessTreeLease struct {
	cmd       *exec.Cmd
	process   *os.Process
	pgid      int
	handedOff bool
	reaped    bool
}

// configureGuardProcessTree 在 exec 前建立独立 Unix 进程组。
func configureGuardProcessTree(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("Guard command is required")
	}
	if cmd.SysProcAttr != nil {
		return errors.New("Guard command process attributes are already configured")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}

// attachGuardProcessTree 绑定 Start 返回的 direct-child handle 与不可复用的 PGID lease。
func attachGuardProcessTree(cmd *exec.Cmd) (*guardProcessTreeLease, error) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return nil, errors.New("started Guard direct-child process is required")
	}
	lease := &guardProcessTreeLease{cmd: cmd, process: cmd.Process, pgid: cmd.Process.Pid}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return lease, err
	}
	if pgid != cmd.Process.Pid {
		return lease, errors.New("started Guard did not enter its exact process group")
	}
	return lease, nil
}

// stopGuardProcessTree 先给 Guard 清理 helper 的机会，再强杀整组并同步回收 direct child。
func stopGuardProcessTree(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if err := validateGuardProcessTreeLease(cmd, lease); err != nil {
		return err
	}
	if lease.handedOff {
		return errors.New("Guard process tree lease was already handed off")
	}
	if lease.reaped {
		return nil
	}
	termErr := signalGuardProcessGroup(lease.pgid, syscall.SIGTERM)
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), pkglogger.Get(), "updater.guardProcessTree.wait", func(context.Context) {
		waitDone <- normalizeGuardProcessWait(cmd.Wait())
	})
	if termErr != nil {
		killErr := signalGuardProcessGroup(lease.pgid, syscall.SIGKILL)
		waitErr := <-waitDone
		lease.reaped = true
		return errors.Join(termErr, killErr, waitErr)
	}
	timer := time.NewTimer(guardProcessTreeGracePeriod)
	defer timer.Stop()
	select {
	case waitErr := <-waitDone:
		lease.reaped = true
		return waitErr
	case <-timer.C:
	}
	killErr := signalGuardProcessGroup(lease.pgid, syscall.SIGKILL)
	waitErr := <-waitDone
	lease.reaped = true
	return errors.Join(killErr, waitErr)
}

// handoffGuardProcessTree 释放 direct-child handle，并永久撤销本 updater 的 PGID 终止权限。
func handoffGuardProcessTree(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if err := validateGuardProcessTreeLease(cmd, lease); err != nil {
		return err
	}
	if lease.reaped {
		return errors.New("reaped Guard process tree cannot be handed off")
	}
	if lease.handedOff {
		return errors.New("Guard process tree lease was already handed off")
	}
	if err := cmd.Process.Release(); err != nil {
		return errors.Join(err, stopGuardProcessTree(cmd, lease))
	}
	lease.handedOff = true
	return nil
}

// validateGuardProcessTreeLease 拒绝仅 PID 相同但 direct-child handle 已替换的 lease。
func validateGuardProcessTreeLease(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if cmd == nil || cmd.Process == nil || lease == nil {
		return errors.New("Guard process tree direct-child ownership is required")
	}
	if lease.cmd != cmd || lease.process != cmd.Process || lease.pgid != cmd.Process.Pid {
		return errors.New("Guard process tree direct-child ownership does not match")
	}
	return nil
}

// signalGuardProcessGroup 向 exact PGID 发信号，并归一化已消失进程组。
func signalGuardProcessGroup(pgid int, signal syscall.Signal) error {
	if pgid <= 1 {
		return errors.New("Guard process group is invalid")
	}
	err := syscall.Kill(-pgid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// normalizeGuardProcessWait 将树终止导致的退出状态归一化为成功回收。
func normalizeGuardProcessWait(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
