package standalone

import (
	"context"
	"errors"
	"fmt"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
)

// CaptureActiveProcessReceipt 捕获已激活 CLI 的稳定 managed-runtime receipt。
// 它只读进程身份，不启动、重启或终止 provider；expected 提供 owner 已确认的绑定字段。
func (e *Execution) CaptureActiveProcessReceipt(ctx context.Context, agentID string, expected managedruntime.PreparedReceipt, verifier *managedruntime.ProcessReceiptVerifier) (managedruntime.PreparedReceipt, error) {
	if err := validateReceiptCapture(ctx, e, agentID, verifier); err != nil {
		return managedruntime.PreparedReceipt{}, err
	}
	s, err := e.session(agentID)
	if err != nil {
		return managedruntime.PreparedReceipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.activated {
		return managedruntime.PreparedReceipt{}, errors.New("managed runtime receipt capture requires activated session")
	}
	if err := s.readyState(); err != nil {
		return managedruntime.PreparedReceipt{}, err
	}
	pid, err := sessionPID(s)
	if err != nil {
		return managedruntime.PreparedReceipt{}, fmt.Errorf("capture process: %w", err)
	}
	identity, err := verifier.Capture(pid)
	if err != nil {
		return managedruntime.PreparedReceipt{}, err
	}
	if identity.PID != pid {
		return managedruntime.PreparedReceipt{}, managedruntime.ErrProcessReceiptIdentityMismatch
	}
	expected.Process = managedruntime.StableProcessIdentity{PID: int64(identity.PID), ProcessStartToken: identity.ProcessStartToken, ExecutableIdentity: identity.ExecutableIdentity}
	expected.ProviderThreadID = s.threadID
	expected.RuntimeGeneration = int64(s.launchSeq)
	expected.ProcessGeneration = int64(s.launchSeq)
	return managedruntime.FinalizePreparedReceiptDigest(expected)
}

// validateReceiptCapture 检查receipt捕获的基本依赖。
func validateReceiptCapture(ctx context.Context, e *Execution, agentID string, verifier *managedruntime.ProcessReceiptVerifier) error {
	if ctx == nil {
		return errors.New("managed runtime receipt capture requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if e == nil || agentID == "" {
		return errors.New("managed runtime receipt capture requires agent")
	}
	if verifier == nil || verifier.Capture == nil {
		return managedruntime.ErrProductionNoGo
	}
	return nil
}

// sessionPID 获取当前provider进程的PID，不启动或重启进程。
func sessionPID(s *cliSession) (int, error) {
	if s.provider.Name == "claude" {
		return s.claude.PID()
	}
	return s.process.PID()
}
