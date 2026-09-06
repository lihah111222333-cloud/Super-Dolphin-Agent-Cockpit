package orchestrationcommand

import (
	"context"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// storedSnapshot 在结构映射后重算原操作摘要；合法十六进制本身不证明字段绑定正确。
func storedSnapshot(row storedb.OrchestrationCommandJournal) (Snapshot, error) {
	snapshot, err := snapshotFromRow(row)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateLifecycleDigest(snapshotRequest(snapshot), snapshot.OperationDigest); err != nil {
		return Snapshot{}, fmt.Errorf("command journal operation digest mismatch: %w", err)
	}
	return snapshot, nil
}

func snapshotRequest(snapshot Snapshot) TransitionRequest {
	return TransitionRequest{
		CommandID: snapshot.CommandID, OperationID: snapshot.OperationID, ExpectedVersion: snapshot.Version - 1,
		Trigger: snapshot.Trigger, AttemptID: snapshot.AttemptID, EvidenceRef: snapshot.EvidenceRef,
	}
}

// snapshotWithPredecessor 验证读取记录及其直接前驱关系，不把独立合法的终态名称当作合法历史。
func (s *Store) snapshotWithPredecessor(ctx context.Context, row storedb.OrchestrationCommandJournal) (Snapshot, error) {
	snapshot, err := storedSnapshot(row)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.validateJournalPredecessor(ctx, snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// validateJournalPredecessor 只读取精确 version-1；缺前驱立即报损坏，不以当前状态兜底。
func (s *Store) validateJournalPredecessor(ctx context.Context, snapshot Snapshot) error {
	if snapshot.Version == 1 {
		return nil
	}
	row, err := s.queries.GetOrchestrationCommandJournalVersion(ctx, storedb.GetOrchestrationCommandJournalVersionParams{
		CommandID: snapshot.CommandID, Version: snapshot.Version - 1,
	})
	if err != nil {
		return fmt.Errorf("read command journal predecessor: %w", err)
	}
	previous, err := storedSnapshot(row)
	if err != nil {
		return err
	}
	return validateJournalLink(ctx, previous, snapshot)
}

// validateJournalLink 复验相邻版本的状态机与全部派生字段；不声明整库防篡改或权威回执验证。
func validateJournalLink(ctx context.Context, previous, current Snapshot) error {
	if previous.CommandID != current.CommandID || previous.Version != current.Version-1 {
		return ErrInvalidTransition
	}
	plan, err := planTransition(ctx, previous, snapshotRequest(current), current.OperationDigest)
	if err != nil {
		return err
	}
	if plan.NextPhase != current.Phase || plan.NextAcceptResolution != current.AcceptResolution || plan.UncertainPhase != current.UncertainPhase {
		return ErrInvalidTransition
	}
	return nil
}
