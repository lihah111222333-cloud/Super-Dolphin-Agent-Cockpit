package orchestrationcommand

import (
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// prepareLifecycleParams 显式绑定 genesis 的全部 SQL 参数，避免字段静默丢失。
func prepareLifecycleParams(request TransitionRequest, digest string) storedb.PrepareOrchestrationCommandJournalParams {
	return storedb.PrepareOrchestrationCommandJournalParams{
		CommandID: request.CommandID, OperationID: request.OperationID, OperationDigest: digest,
	}
}

// appendLifecycleParams 显式绑定 CAS 前后参数；新 attempt 的唯一性由数据库索引裁决。
func appendLifecycleParams(plan verifiedTransition) storedb.AppendOrchestrationCommandJournalParams {
	request := plan.Request
	nextAttemptID := plan.Current.AttemptID
	if plan.StartsAttempt {
		nextAttemptID = request.AttemptID
	}
	return storedb.AppendOrchestrationCommandJournalParams{
		CommandID: request.CommandID, ExpectedVersion: request.ExpectedVersion,
		ExpectedPhase: string(plan.Current.Phase), ExpectedAttemptID: plan.Current.AttemptID,
		ExpectedAcceptResolution: plan.Current.AcceptResolution,
		NextPhase:                string(plan.NextPhase), NextAcceptResolution: plan.NextAcceptResolution,
		NextAttemptID: nextAttemptID, UncertainPhase: string(plan.UncertainPhase),
		Trigger: string(request.Trigger), OperationID: request.OperationID,
		OperationDigest: plan.OperationDigest, EvidenceRef: request.EvidenceRef,
	}
}

// snapshotFromRow 将 SQLC row 的每个字段显式映射为领域快照，并在返回前阻断损坏记录。
func snapshotFromRow(row storedb.OrchestrationCommandJournal) (Snapshot, error) {
	recordedAt, err := time.Parse(time.RFC3339Nano, row.RecordedAt)
	if err != nil || recordedAt.IsZero() {
		return Snapshot{}, fmt.Errorf("command journal recorded_at is invalid")
	}
	snapshot := Snapshot{
		CommandID: row.CommandID, CommandVersion: row.CommandVersion, Version: row.Version,
		OperationID: row.OperationID, OperationDigest: row.OperationDigest,
		Phase: Phase(row.Phase), AcceptResolution: row.AcceptResolution, AttemptID: row.AttemptID,
		Trigger: Trigger(row.Trigger), EvidenceRef: row.EvidenceRef,
		UncertainPhase: Phase(row.UncertainPhase), SchemaVersion: row.SchemaVersion,
		RecordedAt: recordedAt,
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// validateSnapshot 串行执行快照 header、genesis、证据和 attempt-shape 校验。
func validateSnapshot(snapshot Snapshot) error {
	for _, check := range []func(Snapshot) error{
		validateSnapshotHeader, validateSnapshotPhase, validateSnapshotGenesis, validateSnapshotEvidence,
		validateSnapshotUncertainty,
		validateSnapshotAttempt,
	} {
		if err := check(snapshot); err != nil {
			return err
		}
	}
	return nil
}

// validateSnapshotHeader 校验身份、版本以及由日志注册表列举的阶段和决议。
func validateSnapshotHeader(snapshot Snapshot) error {
	if err := validateText("command_id", snapshot.CommandID, 128); err != nil {
		return err
	}
	if err := validateText("operation_id", snapshot.OperationID, 128); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", snapshot.OperationDigest); err != nil {
		return err
	}
	if snapshot.SchemaVersion != 1 || snapshot.CommandVersion != 1 {
		return fmt.Errorf("command journal schema or command version is unsupported")
	}
	if snapshot.Version <= 0 {
		return fmt.Errorf("command journal version is invalid: %d", snapshot.Version)
	}
	return nil
}

func validateSnapshotPhase(snapshot Snapshot) error {
	if !knownPhase(snapshot.Phase) {
		return fmt.Errorf("command journal phase is unknown: %q", snapshot.Phase)
	}
	if snapshot.Trigger != TriggerPrepare && !knownJournalTransition(snapshot) {
		return fmt.Errorf("command journal trigger is unknown: %q", snapshot.Trigger)
	}
	return validateAcceptResolution(snapshot.Phase, snapshot.AcceptResolution)
}

// validateSnapshotGenesis 区分唯一 version=1 genesis 与后续可追溯转换。
func validateSnapshotGenesis(snapshot Snapshot) error {
	if snapshot.Version == 1 {
		return validateGenesisShape(snapshot)
	}
	if snapshot.Trigger == TriggerPrepare || !knownJournalTransition(snapshot) {
		return fmt.Errorf("non-genesis command journal transition is unknown")
	}
	return nil
}

// validateGenesisShape 固化 version=1 的唯一 prepare_accept/custody-pending 形状。
func validateGenesisShape(snapshot Snapshot) error {
	if snapshot.Phase != PhaseCustodyPending || snapshot.AcceptResolution != "pending" ||
		snapshot.AttemptID != "" || snapshot.Trigger != TriggerPrepare ||
		snapshot.EvidenceRef != "" || snapshot.UncertainPhase != "" {
		return fmt.Errorf("command journal genesis fields are invalid")
	}
	return nil
}

func validateSnapshotEvidence(snapshot Snapshot) error {
	if snapshot.Version > 1 {
		if err := validateText("evidence_ref", snapshot.EvidenceRef, 512); err != nil {
			return err
		}
	}
	return nil
}

// validateSnapshotUncertainty 只允许 outcome_unknown 绑定三种已知前置阶段。
func validateSnapshotUncertainty(snapshot Snapshot) error {
	if snapshot.Phase == PhaseUnknown {
		switch snapshot.UncertainPhase {
		case PhaseTokenIssued, PhaseEffectStarted, PhaseProviderAcknowledged:
		default:
			return fmt.Errorf("outcome_unknown uncertain_phase is invalid: %q", snapshot.UncertainPhase)
		}
	} else if snapshot.UncertainPhase != "" {
		return fmt.Errorf("non-unknown command journal has uncertain_phase %q", snapshot.UncertainPhase)
	}
	return nil
}

// validateSnapshotAttempt 校验派发前、派发后、拒绝与 abort 分支的 attempt presence。
func validateSnapshotAttempt(snapshot Snapshot) error {
	if snapshot.AttemptID != "" {
		if err := validateText("attempt_id", snapshot.AttemptID, 128); err != nil {
			return err
		}
	}
	switch snapshot.Phase {
	case PhaseCustodyPending, PhaseAccepted:
		if snapshot.AttemptID != "" {
			return fmt.Errorf("pre-dispatch command journal has attempt_id")
		}
	case PhaseRejected:
		if snapshot.AcceptResolution == "aborted" && snapshot.AttemptID != "" {
			return fmt.Errorf("aborted rejection command journal has attempt_id")
		}
	default:
		if snapshot.AttemptID == "" {
			return fmt.Errorf("post-dispatch command journal requires attempt_id")
		}
	}
	return nil
}

func knownJournalTransition(snapshot Snapshot) bool {
	for _, rule := range phaseTransitions() {
		if rule.Trigger == snapshot.Trigger && rule.To == snapshot.Phase {
			return true
		}
	}
	return false
}

// validateAcceptResolution 保证 pending、accepted、aborted 与阶段保持完整关联。
func validateAcceptResolution(phase Phase, resolution string) error {
	switch resolution {
	case "pending":
		if phase != PhaseCustodyPending {
			return fmt.Errorf("pending accept resolution is only valid for custody-pending")
		}
	case "accepted":
		if phase == PhaseCustodyPending {
			return fmt.Errorf("accepted resolution cannot remain custody-pending")
		}
	case "aborted":
		if phase != PhaseRejected {
			return fmt.Errorf("aborted accept resolution requires rejected phase")
		}
	default:
		return fmt.Errorf("accept resolution is unknown: %q", resolution)
	}
	return nil
}
