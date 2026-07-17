package pidregistry

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	exactTerminationGrace = 2 * time.Second
	exactTerminationPoll  = 50 * time.Millisecond
)

var (
	ErrStableProcessIdentityMismatch      = errors.New("stable process identity mismatch")
	ErrStableProcessIdentityRead          = errors.New("stable process identity read failed")
	ErrStableProcessNotFound              = errors.New("stable process no longer exists")
	ErrExactProcessTerminationTimeout     = errors.New("exact process termination timed out")
	ErrExactProcessTerminationUnsupported = errors.New("exact process termination is unsupported on this platform")
)

// StableProcessIdentity 是跨 PID reuse 稳定的内核进程身份。
type StableProcessIdentity struct {
	PID                 int
	ProcessStartToken   string
	ExecutableIdentity  string
	TerminationEndpoint string
	TerminationToken    string
}

// CaptureStableProcessIdentity 从平台内核捕获 PID 的创建令牌和可执行文件身份。
func CaptureStableProcessIdentity(pid int) (StableProcessIdentity, error) {
	identity, err := readProcessIdentity(pid)
	if err != nil {
		exists, existsErr := exactProcessExists(pid)
		if existsErr == nil && !exists {
			return StableProcessIdentity{}, errors.Join(ErrStableProcessNotFound, err)
		}
		if existsErr != nil {
			return StableProcessIdentity{}, errors.Join(ErrStableProcessIdentityRead, err, existsErr)
		}
		return StableProcessIdentity{}, errors.Join(ErrStableProcessIdentityRead, err)
	}
	return StableProcessIdentity{
		PID: pid, ProcessStartToken: identity.startToken, ExecutableIdentity: identity.executable,
	}, nil
}

// TerminateExactProcess 通过认证协作端点请求 exact process 自行退出，不发送 PID signal。
func TerminateExactProcess(ctx context.Context, identity StableProcessIdentity) error {
	if err := RequestExactProcessTermination(ctx, identity); err != nil {
		return err
	}
	return waitForCooperativeProcessExit(ctx, identity, time.Now().Add(exactTerminationGrace))
}

// RequestExactProcessTermination 校验稳定身份后向其认证端点发送终止请求。
func RequestExactProcessTermination(ctx context.Context, identity StableProcessIdentity) error {
	if !stableProcessIdentityComplete(identity) ||
		strings.TrimSpace(identity.TerminationEndpoint) == "" ||
		strings.TrimSpace(identity.TerminationToken) == "" {
		return errors.New("cooperative termination requires stable identity, endpoint, and token")
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	current, err := CaptureStableProcessIdentity(identity.PID)
	if err != nil {
		return err
	}
	if current.ProcessStartToken != identity.ProcessStartToken ||
		current.ExecutableIdentity != identity.ExecutableIdentity {
		return ErrStableProcessIdentityMismatch
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return requestCooperativeTermination(ctx, identity.TerminationEndpoint, identity.TerminationToken)
}

// waitForCooperativeProcessExit 只观察原稳定身份消失，不向可能复用的 PID 发信号。
func waitForCooperativeProcessExit(ctx context.Context, identity StableProcessIdentity, deadline time.Time) error {
	var lastIdentityReadErr error
	for {
		gone, readErr, err := observeCooperativeProcess(identity)
		if err != nil {
			return err
		}
		if gone {
			return nil
		}
		lastIdentityReadErr = readErr
		if !time.Now().Before(deadline) {
			return cooperativeTerminationTimeout(lastIdentityReadErr)
		}
		if err := waitExactProcessPoll(ctx, exactTerminationPoll); err != nil {
			return err
		}
	}
}

func observeCooperativeProcess(identity StableProcessIdentity) (bool, error, error) {
	current, err := CaptureStableProcessIdentity(identity.PID)
	if errors.Is(err, ErrStableProcessNotFound) {
		return true, nil, nil
	}
	if err != nil {
		if errors.Is(err, ErrStableProcessIdentityRead) {
			return false, err, nil
		}
		return false, nil, err
	}
	changed := current.ProcessStartToken != identity.ProcessStartToken || current.ExecutableIdentity != identity.ExecutableIdentity
	return changed, nil, nil
}

func cooperativeTerminationTimeout(readErr error) error {
	if readErr != nil {
		return errors.Join(ErrExactProcessTerminationTimeout, readErr)
	}
	return ErrExactProcessTerminationTimeout
}

func stableProcessIdentityComplete(identity StableProcessIdentity) bool {
	return identity.PID > 1 &&
		strings.TrimSpace(identity.ProcessStartToken) != "" &&
		strings.TrimSpace(identity.ExecutableIdentity) != ""
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
