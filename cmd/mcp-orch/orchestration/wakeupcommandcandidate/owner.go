package wakeupcommandcandidate

import "fmt"

// Accept 原子保存命令；同 key 同 digest 只允许完整相等的 replay。
func (o *Owner) Accept(command WakeupCommand) (WakeupCommand, bool, error) {
	if o == nil {
		return WakeupCommand{}, false, ErrInvalidCommand
	}
	if err := command.validate(); err != nil {
		return WakeupCommand{}, false, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	key := command.Intent.Key
	if existing, ok := o.byKey[key]; ok {
		if existing.Intent.Digest != command.Intent.Digest {
			return WakeupCommand{}, false, ErrDigestConflict
		}
		if existing != command {
			return WakeupCommand{}, false, ErrReplayConflict
		}
		return existing, true, nil
	}
	if existing, ok := o.byID[command.CommandID]; ok && existing != command {
		return WakeupCommand{}, false, ErrCommandConflict
	}
	o.byKey[key] = command
	o.byID[command.CommandID] = command
	return command, false, nil
}

// Find 按原始 intent key/digest 找回命令，不创建缺失记录或新 attempt。
func (o *Owner) Find(intent Intent) (WakeupCommand, error) {
	if o == nil {
		return WakeupCommand{}, ErrNotFound
	}
	if err := intent.validate(); err != nil {
		return WakeupCommand{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	command, ok := o.byKey[intent.Key]
	if !ok {
		return WakeupCommand{}, ErrNotFound
	}
	if command.Intent.Digest != intent.Digest {
		return WakeupCommand{}, ErrDigestConflict
	}
	return command, nil
}

// IssueDispatch 先记录唯一 attempt/receipt/permit/nonce，再允许调用方触发 provider。
// 已存在但尚未落定结果的 attempt 只允许原 attempt 查询，重放 fail-closed。
func (o *Owner) IssueDispatch(commandID, wakeupFence string) (Attempt, error) {
	command, err := o.commandForDispatch(commandID, wakeupFence)
	if err != nil {
		return Attempt{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if previous, ok := o.attempts[commandID]; ok {
		switch previous.Phase {
		case PhaseEffectPending, PhaseOutcomeUnknown:
			// A pending attempt may already have crossed the external effect
			// boundary even though its outcome has not been observed. Returning
			// its permit on replay would let a caller invoke the provider twice.
			// The original attempt remains queryable through Reconcile; replay
			// must not authorize another effect.
			return Attempt{}, ErrAttemptUnresolved
		default:
			return Attempt{}, ErrAttemptTerminal
		}
	}
	attempt := newAttempt(command)
	o.attempts[commandID] = attempt
	return attempt, nil
}

// commandForDispatch 读取并核对 command 与当前 wakeup fence。
func (o *Owner) commandForDispatch(commandID, fence string) (WakeupCommand, error) {
	if o == nil {
		return WakeupCommand{}, ErrNotFound
	}
	if err := boundedText("command_id", commandID, 256); err != nil {
		return WakeupCommand{}, err
	}
	if err := boundedText("wakeup_fence", fence, 256); err != nil {
		return WakeupCommand{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	command, ok := o.byID[commandID]
	if !ok {
		return WakeupCommand{}, ErrNotFound
	}
	if command.WakeupFence != fence {
		return WakeupCommand{}, ErrStaleFence
	}
	return command, nil
}

func newAttempt(command WakeupCommand) Attempt {
	base := fmt.Sprintf("%s:%d", command.CommandID, command.AttemptNumber)
	return Attempt{CommandID: command.CommandID, WakeupID: command.WakeupID, WakeupFence: command.WakeupFence,
		IntentKey: command.Intent.Key, IntentDigest: command.Intent.Digest, AttemptNumber: command.AttemptNumber,
		DispatchReceipt: "dispatch-receipt:" + base, Permit: "dispatch-permit:" + base, Nonce: "dispatch-nonce:" + base,
		Phase: PhaseEffectPending, Outcome: OutcomePending}
}

// ValidatePermit 是 provider side-effect 前的最后一道本地合同检查，不执行副作用。
func (o *Owner) ValidatePermit(permit DispatchPermit) error {
	if o == nil {
		return ErrPermitMismatch
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	attempt, ok := o.attempts[permit.CommandID]
	if !ok || attempt.Phase != PhaseEffectPending || !permitMatches(attempt, permit) {
		return ErrPermitMismatch
	}
	return nil
}

// permitMatches 比较 provider 请求携带的全部原始绑定材料。
func permitMatches(attempt Attempt, permit DispatchPermit) bool {
	return attempt.WakeupID == permit.WakeupID &&
		attempt.WakeupFence == permit.WakeupFence &&
		attempt.AttemptNumber == permit.AttemptNumber &&
		attempt.DispatchReceipt == permit.DispatchReceipt &&
		attempt.Permit == permit.Permit && attempt.Nonce == permit.Nonce
}

// MarkProviderOutcome 在 provider 返回后写入 accepted、unknown 或 definitive failure。
func (o *Owner) MarkProviderOutcome(request OutcomeRequest) (Attempt, error) {
	return o.markOutcome(request, false)
}

// ResolveUnknown 只解析已有 unknown attempt；它绝不创建新 attempt 或重新调用 provider。
func (o *Owner) ResolveUnknown(request OutcomeRequest) (Attempt, error) {
	return o.markOutcome(request, true)
}

// markOutcome 在锁内收敛 mark-after-effect 或原 attempt 的 reconcile 结果。
func (o *Owner) markOutcome(request OutcomeRequest, reconcile bool) (Attempt, error) {
	if err := request.validate(); err != nil {
		return Attempt{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	attempt, ok := o.attempts[request.CommandID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	if attempt.WakeupFence != request.WakeupFence {
		return Attempt{}, ErrStaleFence
	}
	if !permitMatches(attempt, request.permit()) {
		return Attempt{}, ErrPermitMismatch
	}
	if request.Outcome == OutcomeUnknown {
		return markUnknown(attempt, reconcile, o.attempts)
	}
	return markKnown(attempt, request, reconcile, o.attempts)
}

func (r OutcomeRequest) permit() DispatchPermit {
	return DispatchPermit{CommandID: r.CommandID, WakeupID: r.WakeupID, WakeupFence: r.WakeupFence,
		AttemptNumber: r.AttemptNumber, DispatchReceipt: r.DispatchReceipt, Permit: r.Permit, Nonce: r.Nonce}
}

// markUnknown 只允许 pending 窗口进入 unknown，reconcile 不得伪造未知结果。
func markUnknown(attempt Attempt, reconcile bool, attempts map[string]Attempt) (Attempt, error) {
	if reconcile || attempt.Phase != PhaseEffectPending {
		return Attempt{}, ErrInvalidOutcome
	}
	attempt.Phase, attempt.Outcome = PhaseOutcomeUnknown, OutcomeUnknown
	attempts[attempt.CommandID] = attempt
	return attempt, nil
}

// markKnown 写入 accepted/definitive failure，unknown 只能由原 attempt reconcile 进入。
func markKnown(attempt Attempt, request OutcomeRequest, reconcile bool, attempts map[string]Attempt) (Attempt, error) {
	if reconcile {
		if attempt.Phase != PhaseOutcomeUnknown {
			return Attempt{}, ErrAttemptUnresolved
		}
	} else if attempt.Phase != PhaseEffectPending {
		if attempt.Outcome == request.Outcome {
			return attempt, nil
		}
		return Attempt{}, ErrOutcomeConflict
	}
	if request.Outcome == OutcomeAccepted {
		attempt.Phase = PhaseProviderAccepted
	} else {
		attempt.Phase = PhaseProviderFailed
	}
	attempt.Outcome = request.Outcome
	attempts[attempt.CommandID] = attempt
	return attempt, nil
}

// validate 检查结果请求携带的 command/wakeup/attempt 和回执材料。
func (r OutcomeRequest) validate() error {
	if err := boundedText("command_id", r.CommandID, 256); err != nil {
		return err
	}
	if err := boundedText("wakeup_id", r.WakeupID, 256); err != nil {
		return err
	}
	if err := boundedText("wakeup_fence", r.WakeupFence, 256); err != nil {
		return err
	}
	if err := validateAttemptBinding(r); err != nil {
		return err
	}
	return validateOutcome(r.Outcome)
}

func validateAttemptBinding(r OutcomeRequest) error {
	if r.AttemptNumber != 1 || r.DispatchReceipt == "" || r.Permit == "" || r.Nonce == "" {
		return fmt.Errorf("%w: incomplete attempt binding", ErrInvalidOutcome)
	}
	return nil
}

func validateOutcome(outcome ProviderOutcome) error {
	if outcome == OutcomeAccepted || outcome == OutcomeUnknown || outcome == OutcomeDefinitiveFailure {
		return nil
	}
	return fmt.Errorf("%w: %q", ErrInvalidOutcome, outcome)
}

// Reclaim 将未标记的窗口置为 unknown，并只返回原 attempt；不递增 attempt number。
func (o *Owner) Reclaim(commandID, wakeupFence string) (Attempt, error) {
	if _, err := o.commandForDispatch(commandID, wakeupFence); err != nil {
		return Attempt{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	attempt, ok := o.attempts[commandID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	if attempt.Phase == PhaseEffectPending {
		attempt.Phase, attempt.Outcome = PhaseOutcomeUnknown, OutcomeUnknown
		o.attempts[commandID] = attempt
	}
	return attempt, nil
}

// Reconcile 只查询原 attempt，供 lease reclaim 或进程重启后的外部回执核对使用。
func (o *Owner) Reconcile(commandID string, attemptNumber int) (Attempt, error) {
	if o == nil {
		return Attempt{}, ErrNotFound
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	attempt, ok := o.attempts[commandID]
	if !ok || attempt.AttemptNumber != attemptNumber {
		return Attempt{}, ErrNotFound
	}
	return attempt, nil
}

// Snapshot 返回当前 attempt；它不是新的执行许可。
func (o *Owner) Snapshot(commandID string) (Attempt, error) {
	return o.Reconcile(commandID, 1)
}
