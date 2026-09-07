package orchestration

import (
	"context"
	"errors"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

type nativeReceiptLauncher interface {
	SubmitTurnWithReceipt(context.Context, *agentRuntime, TurnSubmission) (contract.ProviderSubmissionReceipt, error)
}

// SubmitTurnWithReceipt 仅接收真实 CLI 确认，并复用已有提交代际与早到终态围栏。
func (s *service) SubmitTurnWithReceipt(ctx context.Context, req TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	var receipt contract.ProviderSubmissionReceipt
	if s == nil || s.turns == nil {
		return receipt, errors.New("turn controller is not configured")
	}
	launcher, ok := s.turns.launcher.(nativeReceiptLauncher)
	if !ok {
		return receipt, errors.New("native provider acknowledgement is unavailable")
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return receipt, errors.New("agent id is required")
	}
	if s.turns.rehydrator != nil {
		s.turns.rehydrator.ensureRuntimeForPersistedAgent(ctx, agentID)
	}
	handled, terminals, ref, err := s.turns.trySubmitRemoteTurn(ctx, agentID, req, nativeReceiptSubmitter(launcher, &receipt))
	err = errors.Join(err, s.settleRemoteTurnSubmission(ctx, ref, terminals))
	if err != nil {
		return contract.ProviderSubmissionReceipt{}, err
	}
	if !handled {
		return contract.ProviderSubmissionReceipt{}, errors.New("native provider acknowledgement requires a running CLI receiver")
	}
	return receipt, nil
}

// validateNativeReceiptFence 拒绝跨 agent、thread 或进程代际的迟到确认。
func validateNativeReceiptFence(receipt contract.ProviderSubmissionReceipt, agent *agentRuntime) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if agent == nil || receipt.AgentID != agent.id || receipt.ThreadID != agent.remoteThreadID || receipt.LaunchSeq != agent.launchSeq || receipt.Provider != agent.provider {
		return errors.New("native provider acknowledgement execution fence mismatch")
	}
	return nil
}

// SubmitTurnWithReceipt 要求独立执行器返回 typed 协议确认，不将旧 error-only 返回值转换为确认。
func (l *cliLauncher) SubmitTurnWithReceipt(ctx context.Context, _ *agentRuntime, req TurnSubmission) (contract.ProviderSubmissionReceipt, error) {
	backend, ok := l.backend.(interface {
		SubmitTurnWithReceipt(context.Context, contract.TurnSubmission) (contract.ProviderSubmissionReceipt, error)
	})
	if !ok {
		return contract.ProviderSubmissionReceipt{}, errors.New("CLI execution backend has no native acknowledgement capability")
	}
	return backend.SubmitTurnWithReceipt(ctx, req)
}

// nativeReceiptSubmitter 在复用提交围栏前校验真实确认的身份。
func nativeReceiptSubmitter(launcher nativeReceiptLauncher, receipt *contract.ProviderSubmissionReceipt) func(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return func(ctx context.Context, agent *agentRuntime, request TurnSubmission) (string, error) {
		result, err := launcher.SubmitTurnWithReceipt(ctx, agent, request)
		if err != nil {
			return "", err
		}
		if err := validateNativeReceiptFence(result, agent); err != nil {
			return "", err
		}
		*receipt = result
		return result.TurnID, nil
	}
}
