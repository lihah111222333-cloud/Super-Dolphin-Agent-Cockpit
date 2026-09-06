package retentioncandidate

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// CandidateDisposition 表示候选游标相对于目标 floor 的关系。
type CandidateDisposition string

const (
	CandidatePinned CandidateDisposition = "pinned"
	CandidateStale  CandidateDisposition = "stale"
)

// CandidateEntry 是留存裁决使用的有界不可变投影，只含元数据，不携带正文或删除能力。
type CandidateEntry struct {
	PinKind             string               `json:"pin_kind"`
	PinKey              string               `json:"pin_key"`
	SourceID            string               `json:"source_id"`
	SourceEpoch         string               `json:"source_epoch"`
	Lane                string               `json:"lane"`
	AfterSourceSequence int64                `json:"after_source_sequence"`
	LifecycleStatus     string               `json:"lifecycle_status"`
	OperationStatus     string               `json:"operation_status"`
	SchemaVersion       int64                `json:"schema_version"`
	Disposition         CandidateDisposition `json:"disposition"`
}

// IndexSnapshot 是事务绑定的候选索引，不是权威事实，不得持久化或执行物理 GC。
type IndexSnapshot struct {
	SourceID      string           `json:"source_id"`
	SourceEpoch   string           `json:"source_epoch"`
	Lane          eventstore.Lane  `json:"lane"`
	ExpectedFloor int64            `json:"expected_floor"`
	Entries       []CandidateEntry `json:"entries"`
	PinnedCount   int              `json:"pinned_count"`
}

// GCDisposition 只允许阻断结果；本地候选不能证明 custody、checkpoint 或 legal-hold。
type GCDisposition string

const (
	GCBlockedPinned     GCDisposition = "blocked_pinned"
	GCBlockedUnverified GCDisposition = "blocked_unverified"
)

// GCPlan 是解释性、不可执行的结果；不提供删除方法，调用方必须另取权威证明。
type GCPlan struct {
	Disposition    GCDisposition   `json:"disposition"`
	SourceID       string          `json:"source_id"`
	SourceEpoch    string          `json:"source_epoch"`
	Lane           eventstore.Lane `json:"lane"`
	ExpectedFloor  int64           `json:"expected_floor"`
	CandidateCount int             `json:"candidate_count"`
	PinnedCount    int             `json:"pinned_count"`
}

// BuildIndexSnapshotTx 在调用方事务中读取并校验有界候选集，未知或格式异常行直接阻断。
func (v *Verifier) BuildIndexSnapshotTx(ctx context.Context, dbtx storedb.DBTX, req eventstore.TombstoneRequest) (IndexSnapshot, error) {
	if err := validateVerificationInputs(v, ctx, dbtx, req); err != nil {
		return IndexSnapshot{}, err
	}
	rows, err := loadCandidates(ctx, dbtx, req)
	if err != nil {
		return IndexSnapshot{}, err
	}
	if err := validateCandidateCount(rows); err != nil {
		return IndexSnapshot{}, err
	}
	snapshot := IndexSnapshot{SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: req.Lane, ExpectedFloor: req.ExpectedFloor, Entries: make([]CandidateEntry, 0, len(rows))}
	for _, row := range rows {
		pinned, err := validateCandidate(row, req)
		if err != nil {
			return IndexSnapshot{}, err
		}
		entry := candidateEntry(row, pinned)
		snapshot.Entries = append(snapshot.Entries, entry)
		if pinned {
			snapshot.PinnedCount++
		}
	}
	return snapshot, nil
}

// PlanGC 分类候选结果，但不宣称具备删除权威。
func (s IndexSnapshot) PlanGC() GCPlan {
	disposition := GCBlockedUnverified
	if s.PinnedCount > 0 {
		disposition = GCBlockedPinned
	}
	return GCPlan{Disposition: disposition, SourceID: s.SourceID, SourceEpoch: s.SourceEpoch, Lane: s.Lane, ExpectedFloor: s.ExpectedFloor, CandidateCount: len(s.Entries), PinnedCount: s.PinnedCount}
}

func candidateEntry(row storedb.ListOrchestrationRetentionPinCandidatesRow, pinned bool) CandidateEntry {
	disposition := CandidateStale
	if pinned {
		disposition = CandidatePinned
	}
	return CandidateEntry{PinKind: row.PinKind, PinKey: row.PinKey, SourceID: row.SourceID, SourceEpoch: row.SourceEpoch, Lane: row.Lane, AfterSourceSequence: row.AfterSourceSequence, LifecycleStatus: row.LifecycleStatus, OperationStatus: row.OperationStatus, SchemaVersion: row.SchemaVersion, Disposition: disposition}
}

// verifySnapshot 校验索引快照并保持 EventStore 既有 verifier 合同。
func (v *Verifier) verifySnapshot(ctx context.Context, dbtx storedb.DBTX, req eventstore.TombstoneRequest) error {
	snapshot, err := v.BuildIndexSnapshotTx(ctx, dbtx, req)
	if err != nil {
		return err
	}
	if snapshot.PinnedCount == 0 {
		return nil
	}
	return fmt.Errorf("%w: %d wait cursor(s) retain source sequence %d", eventstore.ErrEventRetentionPinned, snapshot.PinnedCount, req.SourceSequence)
}
