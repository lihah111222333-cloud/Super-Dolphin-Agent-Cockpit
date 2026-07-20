package pidregistry

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	exactTerminationGrace      = 2 * time.Second
	exactTerminationPoll       = 50 * time.Millisecond
	cooperativeCommandReady    = "READY"
	cooperativeCommandPrepare  = "READY"
	cooperativeCommandActivate = "COMMIT"
)

var (
	ErrStableProcessIdentityMismatch       = errors.New("stable process identity mismatch")
	ErrStableProcessIdentityRead           = errors.New("stable process identity read failed")
	ErrStableProcessNotFound               = errors.New("stable process no longer exists")
	ErrExactProcessTerminationTimeout      = errors.New("exact process termination timed out")
	ErrExactProcessTerminationUnsupported  = errors.New("exact process termination is unsupported on this platform")
	ErrCooperativeEndpointNotReady         = errors.New("cooperative endpoint is not ready")
	ErrCooperativeEndpointIdentityMismatch = errors.New("cooperative endpoint identity mismatch")
)

// CooperativeEndpointIdentity 绑定一次 Unix endpoint 发布实例。
type CooperativeEndpointIdentity struct {
	Device           uint64
	Inode            uint64
	UID              uint32
	Mode             uint32
	CreationTimeSec  int64
	CreationTimeNsec int64
}

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
		if errors.Is(err, ErrStableProcessNotFound) || errors.Is(err, ErrStableProcessIdentityMismatch) {
			return StableProcessIdentity{}, err
		}
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

// TerminateExactProcessInstance 仅通过匹配的 endpoint 发布实例终止并等待 exact process。
func TerminateExactProcessInstance(
	ctx context.Context,
	identity StableProcessIdentity,
	endpoint CooperativeEndpointIdentity,
) error {
	if err := RequestExactProcessTerminationInstance(ctx, identity, endpoint); err != nil {
		return err
	}
	return waitForCooperativeProcessExit(ctx, identity, time.Now().Add(exactTerminationGrace))
}

// RequestExactProcessTermination 校验稳定身份后向其认证端点发送终止请求。
func RequestExactProcessTermination(ctx context.Context, identity StableProcessIdentity) error {
	return requestExactProcessTermination(ctx, identity, CooperativeEndpointIdentity{})
}

// RequestExactProcessTerminationInstance 仅终止匹配指定 endpoint 发布实例的 exact process。
func RequestExactProcessTerminationInstance(
	ctx context.Context,
	identity StableProcessIdentity,
	endpoint CooperativeEndpointIdentity,
) error {
	if endpoint == (CooperativeEndpointIdentity{}) {
		return errors.New("cooperative termination requires endpoint identity")
	}
	return requestExactProcessTermination(ctx, identity, endpoint)
}

// requestExactProcessTermination 在 generation fence 内终止指定 endpoint 实例。
func requestExactProcessTermination(
	ctx context.Context,
	identity StableProcessIdentity,
	endpoint CooperativeEndpointIdentity,
) error {
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
	return requestCooperativeTermination(ctx, identity.TerminationEndpoint, identity.TerminationToken, identity.PID, endpoint)
}

// ProbeExactProcessEndpoint 认证 READY，并在请求前后确认 exact process generation 未变化。
func ProbeExactProcessEndpoint(ctx context.Context, identity StableProcessIdentity) error {
	return controlExactProcess(ctx, identity, CooperativeEndpointIdentity{}, cooperativeCommandReady, "READY\n")
}

// ProbeExactProcessEndpointInstance 认证指定发布实例的 READY。
func ProbeExactProcessEndpointInstance(
	ctx context.Context,
	identity StableProcessIdentity,
	endpoint CooperativeEndpointIdentity,
) error {
	if endpoint == (CooperativeEndpointIdentity{}) {
		return errors.New("cooperative READY requires endpoint identity")
	}
	return controlExactProcess(ctx, identity, endpoint, cooperativeCommandReady, "READY\n")
}

// PrepareExactProcessStartup 认证 PREPARE，并保持 helper parked。
func PrepareExactProcessStartup(ctx context.Context, identity StableProcessIdentity, endpoint CooperativeEndpointIdentity) error {
	return controlExactProcess(ctx, identity, endpoint, cooperativeCommandPrepare, "READY\n")
}

// ActivateExactProcessStartup 在 durable ACK 后认证激活 helper，重复调用保持幂等。
func ActivateExactProcessStartup(ctx context.Context, identity StableProcessIdentity, endpoint CooperativeEndpointIdentity) error {
	return controlExactProcess(ctx, identity, endpoint, cooperativeCommandActivate, "COMMITTED\n")
}

// controlExactProcess 将认证控制请求绑定到请求前后的同一进程 generation。
func controlExactProcess(
	ctx context.Context,
	identity StableProcessIdentity,
	endpoint CooperativeEndpointIdentity,
	command, response string,
) error {
	if err := validateCooperativeControlIdentity(identity); err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	before, err := captureMatchingStableIdentity(identity)
	if err != nil {
		return err
	}
	if err := requestCooperativeControl(
		ctx, identity.TerminationEndpoint, identity.TerminationToken, command, response, identity.PID, endpoint,
	); err != nil {
		return err
	}
	after, err := captureMatchingStableIdentity(identity)
	if err != nil {
		return err
	}
	if !sameStableProcessGeneration(before, after) {
		return ErrStableProcessIdentityMismatch
	}
	return nil
}

func validateCooperativeControlIdentity(identity StableProcessIdentity) error {
	if !stableProcessIdentityComplete(identity) {
		return errors.New("cooperative control requires stable identity")
	}
	if strings.TrimSpace(identity.TerminationEndpoint) == "" {
		return errors.New("cooperative control requires termination endpoint")
	}
	if strings.TrimSpace(identity.TerminationToken) == "" {
		return errors.New("cooperative control requires termination token")
	}
	return nil
}

func captureMatchingStableIdentity(want StableProcessIdentity) (StableProcessIdentity, error) {
	got, err := CaptureStableProcessIdentity(want.PID)
	if err != nil {
		return StableProcessIdentity{}, err
	}
	if !sameStableProcessGeneration(want, got) {
		return StableProcessIdentity{}, ErrStableProcessIdentityMismatch
	}
	return got, nil
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
