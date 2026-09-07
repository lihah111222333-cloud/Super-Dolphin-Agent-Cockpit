package standalone

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/codexwire"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// InterruptTurn 只中断指定执行代际和 turn，不能把旧清理请求重定向到当前轮。
func (e *Execution) InterruptTurn(ctx context.Context, agentID string, ref turndto.TurnRefV1, launchSeq uint64, _ string) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	if ref.ThreadID == "" || ref.TurnID == "" || launchSeq == 0 {
		return errors.New("interrupt requires complete turn and launch identity")
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	process, target, err := session.captureInterrupt(&ref, launchSeq)
	if err != nil {
		return err
	}
	return interruptCapturedTurn(ctx, process, target)
}

// captureInterrupt 在同一锁内校验并冻结 process 和目标身份；锁外不能重查 agent。
func (s *cliSession) captureInterrupt(expected *turndto.TurnRefV1, launchSeq uint64) (*codexwire.Process, turndto.TurnRefV1, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := turndto.TurnRefV1{ThreadID: s.threadID, TurnID: s.turnID}
	if expected != nil && (*expected != ref || launchSeq != s.launchSeq) {
		return nil, ref, errors.New("interrupt turn or launch identity mismatch")
	}
	if s.provider.Name == "claude" {
		return nil, ref, unsupportedCLICapability(providerdto.CapTurnInterrupt, s.provider.Name)
	}
	if s.stopped || s.process == nil || ref.ThreadID == "" || ref.TurnID == "" {
		return nil, ref, errors.New("CLI agent has no interruptible turn")
	}
	s.interruptRequestID = shared.NewID("orch-interrupt")
	return s.process, ref, nil
}

// interruptCapturedTurn 只向捕获的旧 process 发 RPC，代际替换不会改变目标。
func interruptCapturedTurn(ctx context.Context, process *codexwire.Process, ref turndto.TurnRefV1) error {
	var result map[string]any
	return process.Client.CallResult(ctx, "turn/interrupt", map[string]string{"threadId": ref.ThreadID, "turnId": ref.TurnID}, &result)
}
