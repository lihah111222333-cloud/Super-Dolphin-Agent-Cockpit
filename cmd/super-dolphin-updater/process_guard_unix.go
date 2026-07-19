//go:build darwin || linux

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

const (
	guardProcessTreeGracePeriod = 500 * time.Millisecond
	guardProcessTreeLeaseEnv    = "SUPER_DOLPHIN_UPDATER_GUARD_TREE_LEASE"
	guardProcessTreeLeaseReady  = "READY"
	guardProcessTreeLeaseWait   = 2 * time.Second
)

// guardProcessTreeLease keeps an exact, TERM-ignoring process in the Guard group
// until READY succeeds or failure cleanup has terminated every member.
type guardProcessTreeLease struct {
	cmd             *exec.Cmd
	process         *os.Process
	leaseCmd        *exec.Cmd
	leaseInput      io.WriteCloser
	leaseIdentity   pidregistry.StableProcessIdentity
	pgid            int
	processReleased bool
	handedOff       bool
	reaped          bool
}

// runGuardProcessTreeLeaseIfRequested runs only in the updater child that pins
// a newly-created process group during Guard READY setup.
func runGuardProcessTreeLeaseIfRequestedPlatform() (bool, int) {
	if os.Getenv(guardProcessTreeLeaseEnv) != "1" {
		return false, 0
	}
	signal.Ignore(syscall.SIGTERM)
	if _, err := fmt.Fprintln(os.Stdout, guardProcessTreeLeaseReady); err != nil {
		return true, 1
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		return true, 1
	}
	return true, 0
}

// configureGuardProcessTree 在 Guard 执行前建立持有稳定身份的组长 lease，再让 Guard 加入该组。
func configureGuardProcessTree(cmd *exec.Cmd) (*guardProcessTreeLease, error) {
	if cmd == nil {
		return nil, errors.New("Guard command is required")
	}
	if cmd.SysProcAttr != nil {
		return nil, errors.New("Guard command process attributes are already configured")
	}
	lease, err := startGuardProcessTreeLease()
	if err != nil {
		return nil, err
	}
	identity, err := pidregistry.CaptureStableProcessIdentity(lease.leaseCmd.Process.Pid)
	if err != nil {
		return nil, errors.Join(fmtGuardLeaseError("capture Guard process tree lease identity", err), cleanupUnverifiedGuardLease(lease))
	}
	lease.leaseIdentity = identity
	if err := verifyGuardProcessTreeLeaseGroup(lease); err != nil {
		return nil, errors.Join(err, stopUnattachedGuardProcessTree(lease))
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pgid: lease.pgid}
	return lease, nil
}

// startGuardProcessTreeLease 启动安装 TERM ignore 后才报告 READY 的临时组长。
func startGuardProcessTreeLease() (*guardProcessTreeLease, error) {
	leaseCmd := exec.Command(os.Args[0])
	leaseInput, err := leaseCmd.StdinPipe()
	if err != nil {
		return nil, fmtGuardLeaseError("open Guard process tree lease input", err)
	}
	leaseCmd.Env = withGuardProcessTreeLeaseEnv(os.Environ())
	leaseCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	leaseOutput, err := leaseCmd.StdoutPipe()
	if err != nil {
		_ = leaseInput.Close()
		return nil, fmt.Errorf("open Guard process tree lease output: %w", err)
	}
	if err := leaseCmd.Start(); err != nil {
		_ = leaseInput.Close()
		return nil, fmtGuardLeaseError("start Guard process tree lease", err)
	}
	lease := &guardProcessTreeLease{leaseCmd: leaseCmd, leaseInput: leaseInput, pgid: leaseCmd.Process.Pid}
	if err := waitGuardProcessTreeLeaseReady(leaseOutput); err != nil {
		return nil, errors.Join(err, cleanupUnverifiedGuardLease(lease))
	}
	return lease, nil
}

func fmtGuardLeaseError(operation string, err error) error {
	return fmt.Errorf("%s: %w", operation, err)
}

func waitGuardProcessTreeLeaseReady(reader io.ReadCloser) error {
	deferredClose := func() error { return reader.Close() }
	defer func() { _ = deferredClose() }()
	deadlineReader, ok := reader.(interface{ SetReadDeadline(time.Time) error })
	if !ok {
		return errors.New("Guard process tree lease output does not support bounded reads")
	}
	if err := deadlineReader.SetReadDeadline(time.Now().Add(guardProcessTreeLeaseWait)); err != nil {
		return fmt.Errorf("set Guard process tree lease readiness deadline: %w", err)
	}
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read Guard process tree lease readiness: %w", err)
	}
	if line != guardProcessTreeLeaseReady+"\n" {
		return errors.New("invalid Guard process tree lease readiness receipt")
	}
	return nil
}

func withGuardProcessTreeLeaseEnv(env []string) []string {
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, guardProcessTreeLeaseEnv+"=") {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, guardProcessTreeLeaseEnv+"=1")
}

// attachGuardProcessTree 绑定已启动 Guard 与经稳定身份确认的 lease 进程组。
func attachGuardProcessTree(cmd *exec.Cmd, lease *guardProcessTreeLease) (*guardProcessTreeLease, error) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return lease, errors.New("started Guard direct-child process is required")
	}
	if err := validateGuardProcessTreeLease(nil, lease); err != nil {
		return lease, err
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return lease, err
	}
	if pgid != lease.pgid {
		return lease, errors.New("started Guard did not enter the leased exact process group")
	}
	lease.cmd = cmd
	lease.process = cmd.Process
	return lease, nil
}

// stopGuardProcessTree 不因 direct Guard 已退出提前返回；lease 持有 PGID 直到整树被杀死。
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
	if termErr == nil {
		time.Sleep(guardProcessTreeGracePeriod)
	}
	if err := validateGuardProcessTreeLease(cmd, lease); err != nil {
		return errors.Join(termErr, err)
	}
	killErr := signalGuardProcessGroup(lease.pgid, syscall.SIGKILL)
	inputErr := lease.leaseInput.Close()
	var guardWaitErr error
	if !lease.processReleased {
		guardWaitErr = normalizeGuardProcessWait(cmd.Wait())
	}
	leaseWaitErr := normalizeGuardProcessWait(lease.leaseCmd.Wait())
	lease.reaped = true
	return errors.Join(termErr, killErr, inputErr, guardWaitErr, leaseWaitErr)
}

// handoffGuardProcessTree 仅在 READY 验证后释放 direct Guard，再关闭临时 lease。
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
		return err
	}
	lease.processReleased = true
	if err := lease.leaseInput.Close(); err != nil {
		return err
	}
	if err := normalizeGuardProcessWait(lease.leaseCmd.Wait()); err != nil {
		return err
	}
	lease.handedOff = true
	return nil
}

func stopUnattachedGuardProcessTree(lease *guardProcessTreeLease) error {
	if lease == nil {
		return nil
	}
	if err := validateGuardProcessTreeLease(nil, lease); err != nil {
		return err
	}
	killErr := signalGuardProcessGroup(lease.pgid, syscall.SIGKILL)
	inputErr := lease.leaseInput.Close()
	waitErr := normalizeGuardProcessWait(lease.leaseCmd.Wait())
	lease.reaped = true
	return errors.Join(killErr, inputErr, waitErr)
}

func cleanupUnverifiedGuardLease(lease *guardProcessTreeLease) error {
	if lease == nil || lease.leaseCmd == nil || lease.leaseCmd.Process == nil || lease.pgid <= 1 {
		return nil
	}
	killErr := signalGuardProcessGroup(lease.pgid, syscall.SIGKILL)
	inputErr := lease.leaseInput.Close()
	waitErr := normalizeGuardProcessWait(lease.leaseCmd.Wait())
	return errors.Join(killErr, inputErr, waitErr)
}

// validateGuardProcessTreeLease 在任何组信号前验证 direct child 所有权和 lease 稳定身份。
func validateGuardProcessTreeLease(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if err := validateGuardProcessTreeLeaseOwnership(cmd, lease); err != nil {
		return err
	}
	current, err := pidregistry.CaptureStableProcessIdentity(lease.leaseCmd.Process.Pid)
	if err != nil {
		return fmtGuardLeaseError("recapture Guard process tree lease identity", err)
	}
	if current != lease.leaseIdentity {
		return errors.New("Guard process tree lease identity changed")
	}
	return verifyGuardProcessTreeLeaseGroup(lease)
}

// validateGuardProcessTreeLeaseOwnership 校验 lease、direct Guard handle 与进程组参数的本地所有权。
func validateGuardProcessTreeLeaseOwnership(cmd *exec.Cmd, lease *guardProcessTreeLease) error {
	if lease == nil || lease.leaseCmd == nil || lease.leaseCmd.Process == nil || lease.leaseInput == nil || lease.pgid <= 1 {
		return errors.New("Guard process tree lease is required")
	}
	if cmd != nil && (lease.cmd != cmd || lease.process != cmd.Process || cmd.Process == nil) {
		return errors.New("Guard process tree direct-child ownership does not match")
	}
	return nil
}

func verifyGuardProcessTreeLeaseGroup(lease *guardProcessTreeLease) error {
	pgid, err := syscall.Getpgid(lease.leaseCmd.Process.Pid)
	if err != nil {
		return fmtGuardLeaseError("recapture Guard process tree lease group", err)
	}
	if pgid != lease.pgid {
		return errors.New("Guard process tree lease group changed")
	}
	return nil
}

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

func normalizeGuardProcessWait(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}
