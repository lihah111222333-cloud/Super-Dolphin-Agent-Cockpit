package pidregistry

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	exactTerminationGrace = 2 * time.Second
	exactTerminationPoll  = 50 * time.Millisecond
)

var (
	ErrStableProcessIdentityMismatch  = errors.New("stable process identity mismatch")
	ErrStableProcessIdentityRead      = errors.New("stable process identity read failed")
	ErrExactProcessTerminationTimeout = errors.New("exact process termination timed out")
)

// StableProcessIdentity 是跨 PID reuse 稳定的内核进程身份。
type StableProcessIdentity struct {
	PID                int
	ProcessStartToken  string
	ExecutableIdentity string
}

type exactProcessOps struct {
	readIdentity  func(int) (processIdentity, error)
	sendTerm      func(int) error
	sendKill      func(int) error
	processExists func(int) (bool, error)
	now           func() time.Time
	wait          func(context.Context, time.Duration) error
}

// CaptureStableProcessIdentity 从平台内核捕获 PID 的创建令牌和可执行文件身份。
func CaptureStableProcessIdentity(pid int) (StableProcessIdentity, error) {
	identity, err := readProcessIdentity(pid)
	if err != nil {
		return StableProcessIdentity{}, errors.Join(ErrStableProcessIdentityRead, err)
	}
	return StableProcessIdentity{
		PID: pid, ProcessStartToken: identity.startToken, ExecutableIdentity: identity.executable,
	}, nil
}

// TerminateExactProcess 只终止匹配 stable identity 的单个进程，并确认其最终退出。
func TerminateExactProcess(ctx context.Context, identity StableProcessIdentity) error {
	return terminateExactProcess(ctx, identity, exactTerminationGrace, exactTerminationPoll, defaultExactProcessOps())
}

func defaultExactProcessOps() exactProcessOps {
	return exactProcessOps{
		readIdentity:  readProcessIdentity,
		sendTerm:      sendSIGTERM,
		sendKill:      sendExactSIGKILL,
		processExists: exactProcessExists,
		now:           time.Now,
		wait:          waitExactProcessPoll,
	}
}

// terminateExactProcess 串行执行 TERM 与 KILL，任何身份不确定性都立即停止。
func terminateExactProcess(
	ctx context.Context,
	identity StableProcessIdentity,
	grace time.Duration,
	poll time.Duration,
	ops exactProcessOps,
) error {
	if err := validateStableProcessIdentity(identity, grace, poll, ops); err != nil {
		return err
	}
	gone, err := verifyStableProcess(identity, ops)
	if err != nil || gone {
		return err
	}
	gone, err = terminateStableProcessWithTERM(ctx, identity, grace, poll, ops)
	if err != nil || gone {
		return err
	}
	return terminateStableProcessWithKILL(ctx, identity, grace, poll, ops)
}

func terminateStableProcessWithTERM(
	ctx context.Context,
	identity StableProcessIdentity,
	grace time.Duration,
	poll time.Duration,
	ops exactProcessOps,
) (bool, error) {
	if err := ops.sendTerm(identity.PID); err != nil {
		return false, fmt.Errorf("terminate exact process with TERM: %w", err)
	}
	return waitForStableProcessExit(ctx, identity, grace, poll, ops)
}

// terminateStableProcessWithKILL 在再次核验身份后发送 KILL，并确认目标退出。
func terminateStableProcessWithKILL(
	ctx context.Context,
	identity StableProcessIdentity,
	grace time.Duration,
	poll time.Duration,
	ops exactProcessOps,
) error {
	gone, err := verifyStableProcess(identity, ops)
	if err != nil || gone {
		return err
	}
	if err := ops.sendKill(identity.PID); err != nil {
		return fmt.Errorf("terminate exact process with KILL: %w", err)
	}
	gone, err = waitForStableProcessExit(ctx, identity, grace, poll, ops)
	if err != nil {
		return err
	}
	if !gone {
		return ErrExactProcessTerminationTimeout
	}
	return nil
}

// validateStableProcessIdentity 校验 stable identity、时间边界与全部进程操作依赖。
func validateStableProcessIdentity(
	identity StableProcessIdentity,
	grace time.Duration,
	poll time.Duration,
	ops exactProcessOps,
) error {
	if !stableProcessIdentityComplete(identity) {
		return errors.New("stable process identity requires PID, start token, and executable identity")
	}
	if grace <= 0 || poll <= 0 || !exactProcessOpsComplete(ops) {
		return errors.New("exact process termination requires positive bounds and complete process ops")
	}
	return nil
}

func stableProcessIdentityComplete(identity StableProcessIdentity) bool {
	return identity.PID > 1 &&
		strings.TrimSpace(identity.ProcessStartToken) != "" &&
		strings.TrimSpace(identity.ExecutableIdentity) != ""
}

// exactProcessOpsComplete 确保 exact termination 的平台操作全部显式注入。
func exactProcessOpsComplete(ops exactProcessOps) bool {
	return ops.readIdentity != nil && ops.sendTerm != nil && ops.sendKill != nil &&
		ops.processExists != nil && ops.now != nil && ops.wait != nil
}

func verifyStableProcess(identity StableProcessIdentity, ops exactProcessOps) (bool, error) {
	exists, err := ops.processExists(identity.PID)
	if err != nil {
		return false, errors.Join(ErrStableProcessIdentityRead, err)
	}
	if !exists {
		return true, nil
	}
	current, err := ops.readIdentity(identity.PID)
	if err != nil {
		return false, errors.Join(ErrStableProcessIdentityRead, err)
	}
	want := processIdentity{startToken: identity.ProcessStartToken, executable: identity.ExecutableIdentity}
	if !processIdentityMatches(want, current) {
		return false, ErrStableProcessIdentityMismatch
	}
	return false, nil
}

// waitForStableProcessExit 有界轮询 stable identity；PID reuse 和读取失败立即返回错误。
func waitForStableProcessExit(
	ctx context.Context,
	identity StableProcessIdentity,
	timeout time.Duration,
	poll time.Duration,
	ops exactProcessOps,
) (bool, error) {
	deadline := ops.now().Add(timeout)
	for {
		gone, err := verifyStableProcess(identity, ops)
		if err != nil || gone {
			return gone, err
		}
		remaining := deadline.Sub(ops.now())
		if remaining <= 0 {
			return false, nil
		}
		if remaining < poll {
			poll = remaining
		}
		if err := ops.wait(ctx, poll); err != nil {
			return false, err
		}
	}
}

func waitExactProcessPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
