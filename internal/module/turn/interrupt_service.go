package turn

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformobs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

// InterruptTurn 向当前线程的 active turn 发送中断，并返回带 envelope 的最终判定状态。
// 没有 active turn 时不报错，而是返回 no_active_turn envelope，方便 UI 幂等收口。
func (s *service) InterruptTurn(ctx context.Context, session contract.Session, source string) (status TurnStatus, err error) {
	status, _, err = s.InterruptTurnForTarget(ctx, session, source, "", "")
	return status, err
}

// InterruptTurnForTarget 仅在 expectedTurnID 仍匹配时捕获当前 handle；不匹配时绝不调用 provider。
func (s *service) InterruptTurnForTarget(ctx context.Context, session contract.Session, source, expectedTurnID, requestID string) (status TurnStatus, accepted bool, err error) {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return TurnStatus{}, false, err
	}
	claim := s.tracker.ClaimInterruptTarget(threadID, expectedTurnID, requestID)
	active := claim.target
	span := s.beginTurnTraceSpan(ctx, "turn.interrupt", threadID, "", active.localID, platformobs.NewCodeAnchor("internal/module/turn/interrupt_service.go", "turn.(*service).InterruptTurn", 11), map[string]any{"source": source})
	ctx = span.ctx
	defer func() { s.finishTurnTraceSpan(span, err) }()
	before := claim.before
	if !claim.found {
		return attachInterruptEnvelope(before, buildTurnInterruptEnvelope(before.State, before.State, false, false, 0, false)), false, nil
	}
	if claim.accepted {
		if isTerminalTurnState(before.State) {
			status := attachInterruptEnvelope(before, buildTurnInterruptTerminalReplayEnvelope(before.State, claim.deliverySent))
			return attachAcceptedInterruptRequestID(status, claim.acceptedRequestID), true, nil
		}
		envelope := buildTurnInterruptRegisteredEnvelope(before.State, before.State)
		if claim.deliverySent {
			envelope = buildTurnInterruptSentPendingEnvelope(before.State, before.State)
		}
		status := attachInterruptEnvelope(before, envelope)
		return attachAcceptedInterruptRequestID(status, claim.acceptedRequestID), true, nil
	}
	if claim.conflict {
		status := attachInterruptEnvelope(before, buildTurnInterruptNotAppliedEnvelope(before.State))
		return attachAcceptedInterruptRequestID(status, claim.acceptedRequestID), false, nil
	}
	if !claim.claimed {
		return before, false, nil
	}
	return s.executeInterruptClaim(ctx, session, active, before, threadID, source, requestID)
}

func (s *service) executeInterruptClaim(ctx context.Context, session contract.Session, active activeTurn, before TurnStatus, threadID, source, requestID string) (TurnStatus, bool, error) {
	if active.handle == nil && active.providerID == "" {
		if !confirmInterruptClaim(s.tracker, active.localID, requestID) {
			releaseInterruptClaim(s.tracker, active.localID, requestID)
			return before, false, nil
		}
		after, ok := s.tracker.Get(active.localID)
		if !ok {
			return TurnStatus{}, false, errors.New("turn/interrupt: preparing turn disappeared after cancellation registration")
		}
		status := attachInterruptEnvelope(after, buildTurnInterruptRegisteredEnvelope(before.State, after.State))
		return attachAcceptedInterruptRequestID(status, requestID), true, nil
	}
	if !s.tracker.claimInterruptDelivery(active.localID, requestID) {
		releaseInterruptClaim(s.tracker, active.localID, requestID)
		return before, false, nil
	}
	start := time.Now()
	waited, err := interruptAndWait(ctx, session, nil, active, threadID, source, requestID, nil)
	if errors.Is(err, contract.ErrInterruptTargetChanged) {
		releaseInterruptClaim(s.tracker, active.localID, requestID)
		return attachInterruptEnvelope(before, buildTurnInterruptNotAppliedEnvelope(before.State)), false, nil
	}
	if err != nil {
		releaseInterruptClaim(s.tracker, active.localID, requestID)
		return TurnStatus{}, false, err
	}
	if !waited {
		releaseInterruptClaim(s.tracker, active.localID, requestID)
		return before, false, nil
	}
	if !s.tracker.acknowledgeInterruptDelivery(active.localID, requestID) {
		return TurnStatus{}, false, errors.New("turn/interrupt: provider delivery acknowledgement was not persisted")
	}
	status, err := s.finishInterrupt(ctx, active, before, start, waited)
	return attachAcceptedInterruptRequestID(status, requestID), true, err
}

// finishInterrupt 在 provider 确认收到中断后等待本地 tracker 收敛，并构造响应 envelope。
// 等待超时时保留 timeout envelope 和当前状态，避免 UI 把“已发送中断”误判成失败启动。
func (s *service) finishInterrupt(
	ctx context.Context,
	active activeTurn,
	before TurnStatus,
	start time.Time,
	waited bool,
) (TurnStatus, error) {
	if waited {
		if err := s.waitForTurnSettle(ctx, active.localID, active.handle); err != nil {
			if status, ok := s.timeoutInterruptStatus(ctx, err, active, before, start); ok {
				return status, err
			}
			return TurnStatus{}, err
		}
	}
	after := s.interruptStatus(active, before, StateInterrupted)
	envelope := buildTurnInterruptEnvelope(
		before.State,
		after.State,
		true,
		true,
		time.Since(start).Milliseconds(),
		interruptConfirmed(after.State),
	)
	return attachInterruptEnvelope(after, envelope), nil
}

// interruptStatus 读取中断后的状态；若 watcher 尚未写回，则用 fallback/handle 合成状态。
func (s *service) interruptStatus(active activeTurn, fallback TurnStatus, defaultState TurnState) TurnStatus {
	if after, ok := s.tracker.Get(active.localID); ok {
		return after
	}
	return TurnStatus{
		LocalID:    active.localID,
		ProviderID: interruptProviderID(fallback, active.handle),
		State:      string(defaultState),
	}
}

// timeoutInterruptStatus 只把本地等待超时转换为响应状态；调用方取消或其他错误继续冒泡。
func (s *service) timeoutInterruptStatus(
	ctx context.Context,
	err error,
	active activeTurn,
	before TurnStatus,
	start time.Time,
) (TurnStatus, bool) {
	if !errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil) {
		return TurnStatus{}, false
	}
	after := s.interruptStatus(active, before, TurnState(before.State))
	return attachInterruptEnvelope(after, buildTurnInterruptTimeoutEnvelope(
		before.State,
		after.State,
		time.Since(start).Milliseconds(),
	)), true
}
