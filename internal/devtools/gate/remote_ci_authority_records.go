package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// RemoteRefreshDeltaRecord 是一次刷新相对已接受 snapshot 的内容寻址增量证据。
type RemoteRefreshDeltaRecord struct {
	JobID               string
	AttemptGeneration   uint64
	AcceptedGeneration  uint64
	AcceptedStateSHA256 string
	AcceptedSnapshotID  string
	DeltaIdentity       string
	DeltaSHA256         string
	DeltaSizeBytes      int64
	TargetTreeSHA       string
	TargetClosureSHA256 string
	TransferMode        cicontract.RefreshTransferMode
	RecordedAt          time.Time
}

// CheckReceiptRecord 是一次远程 CI 必跑检查的不可替代回执。
type CheckReceiptRecord struct {
	RunID              string
	JobID              string
	CandidateTreeSHA   string
	AcceptedGeneration uint64
	AcceptedSnapshotID string
	RequiredCheck      cicontract.RequiredCheck
	Executed           bool
	Passed             bool
	StartedAt          time.Time
	CompletedAt        time.Time
	Duration           time.Duration
	ReceiptSHA256      string
}

type checkReceiptHashPayload struct {
	RunID                string                   `json:"run_id"`
	JobID                string                   `json:"job_id"`
	CandidateTreeSHA     string                   `json:"candidate_tree_sha"`
	AcceptedGeneration   uint64                   `json:"accepted_generation"`
	AcceptedSnapshotID   string                   `json:"accepted_snapshot_id"`
	RequiredCheck        cicontract.RequiredCheck `json:"required_check"`
	Executed             bool                     `json:"executed"`
	Passed               bool                     `json:"passed"`
	StartedAtUnixMilli   int64                    `json:"started_at_unix_ms"`
	CompletedAtUnixMilli int64                    `json:"completed_at_unix_ms"`
	DurationMillis       int64                    `json:"duration_ms"`
}

// CheckReceiptSHA256 计算由真实回执字段唯一决定的内容摘要。
func CheckReceiptSHA256(record CheckReceiptRecord) (string, error) {
	payload, err := json.Marshal(checkReceiptHashPayload{
		RunID: record.RunID, JobID: record.JobID, CandidateTreeSHA: record.CandidateTreeSHA,
		AcceptedGeneration: record.AcceptedGeneration, AcceptedSnapshotID: record.AcceptedSnapshotID,
		RequiredCheck: record.RequiredCheck, Executed: record.Executed, Passed: record.Passed,
		StartedAtUnixMilli: record.StartedAt.UTC().UnixMilli(), CompletedAtUnixMilli: record.CompletedAt.UTC().UnixMilli(),
		DurationMillis: record.Duration.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("encode check receipt hash payload: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func validateRemoteRefreshDeltaRecord(record RemoteRefreshDeltaRecord) error {
	for field, value := range map[string]string{
		"job ID": record.JobID, "accepted state SHA-256": record.AcceptedStateSHA256,
		"accepted snapshot": record.AcceptedSnapshotID, "delta identity": record.DeltaIdentity,
		"target tree": record.TargetTreeSHA, "target closure SHA-256": record.TargetClosureSHA256,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("remote refresh delta %s is required", field)
		}
	}
	if record.AttemptGeneration == 0 || record.AcceptedGeneration == 0 {
		return errors.New("remote refresh delta attempt and accepted generations are required")
	}
	if !isPrefixedSHA256Digest(record.AcceptedStateSHA256) || !isPrefixedSHA256Digest(record.DeltaSHA256) || !isPrefixedSHA256Digest(record.TargetClosureSHA256) {
		return errors.New("remote refresh delta SHA-256 identity is invalid")
	}
	if !validCalibrationOID(record.TargetTreeSHA) {
		return errors.New("remote refresh delta target tree is invalid")
	}
	if record.DeltaSizeBytes <= 0 || record.RecordedAt.IsZero() {
		return errors.New("remote refresh delta size and recorded time are required")
	}
	if err := cicontract.ValidateIncrementalRefreshTransfer(
		record.TransferMode,
		record.AcceptedGeneration,
		record.AcceptedSnapshotID,
		record.DeltaIdentity,
	); err != nil {
		return fmt.Errorf("remote refresh delta transfer: %w", err)
	}
	if err := cicontract.ValidateDeltaRebuild(
		record.TransferMode,
		record.AcceptedGeneration,
		record.AcceptedSnapshotID,
		record.DeltaIdentity,
		record.TargetTreeSHA,
		record.TargetClosureSHA256,
	); err != nil {
		return fmt.Errorf("remote refresh delta rebuild: %w", err)
	}
	return nil
}

func validateCheckReceiptRecord(record CheckReceiptRecord) error {
	for field, value := range map[string]string{
		"run ID": record.RunID, "job ID": record.JobID, "candidate tree": record.CandidateTreeSHA,
		"accepted snapshot": record.AcceptedSnapshotID,
		"receipt SHA-256":   record.ReceiptSHA256,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("check receipt %s is required", field)
		}
	}
	if record.AcceptedGeneration == 0 {
		return errors.New("check receipt accepted generation is required")
	}
	if !validCalibrationOID(record.CandidateTreeSHA) || !isPrefixedSHA256Digest(record.ReceiptSHA256) {
		return errors.New("check receipt identity is invalid")
	}
	if !record.Executed {
		return errors.New("check receipt must record executed=true")
	}
	if record.StartedAt.IsZero() || record.CompletedAt.IsZero() || record.CompletedAt.Before(record.StartedAt) || record.Duration <= 0 || record.CompletedAt.Sub(record.StartedAt) != record.Duration {
		return errors.New("check receipt timing is invalid")
	}
	if !record.StartedAt.Equal(record.StartedAt.Truncate(time.Millisecond)) || !record.CompletedAt.Equal(record.CompletedAt.Truncate(time.Millisecond)) || record.Duration%time.Millisecond != 0 {
		return errors.New("check receipt timing must be millisecond precise")
	}
	expectedSHA256, err := CheckReceiptSHA256(record)
	if err != nil {
		return fmt.Errorf("hash check receipt: %w", err)
	}
	if record.ReceiptSHA256 != expectedSHA256 {
		return errors.New("check receipt SHA-256 does not match receipt content")
	}
	for _, check := range cicontract.RequiredChecks() {
		if record.RequiredCheck == check {
			return nil
		}
	}
	return fmt.Errorf("check receipt required check %q is not canonical", record.RequiredCheck)
}

func validateCompletePassingCheckReceipts(receipts []CheckReceiptRecord) error {
	required := cicontract.RequiredChecks()
	if len(receipts) != len(required) {
		return fmt.Errorf("check receipts count = %d, want %d required checks", len(receipts), len(required))
	}
	seen := make(map[cicontract.RequiredCheck]struct{}, len(receipts))
	var runID, jobID, tree, snapshot string
	var generation uint64
	for index, receipt := range receipts {
		if err := validateCheckReceiptRecord(receipt); err != nil {
			return fmt.Errorf("check receipt[%d]: %w", index, err)
		}
		if !receipt.Passed {
			return fmt.Errorf("check receipt %q did not pass", receipt.RequiredCheck)
		}
		if _, duplicate := seen[receipt.RequiredCheck]; duplicate {
			return fmt.Errorf("check receipt %q is duplicated", receipt.RequiredCheck)
		}
		seen[receipt.RequiredCheck] = struct{}{}
		if index == 0 {
			runID, jobID, tree, generation, snapshot = receipt.RunID, receipt.JobID, receipt.CandidateTreeSHA, receipt.AcceptedGeneration, receipt.AcceptedSnapshotID
			continue
		}
		if receipt.RunID != runID || receipt.JobID != jobID || receipt.CandidateTreeSHA != tree || receipt.AcceptedGeneration != generation || receipt.AcceptedSnapshotID != snapshot {
			return errors.New("check receipts do not bind one run, job, tree, generation, and snapshot")
		}
	}
	for _, check := range required {
		if _, found := seen[check]; !found {
			return fmt.Errorf("required check receipt %q is missing", check)
		}
	}
	return nil
}
