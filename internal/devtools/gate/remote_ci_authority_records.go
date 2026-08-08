package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// CheckReceiptRecord 是一次远程 CI 必跑检查的不可替代回执。
type CheckReceiptRecord struct {
	RunID              string
	JobID              string
	CandidateTreeSHA   string
	AgentTokenDigest   string
	Force              bool
	AcceptedGeneration uint64
	AcceptedSnapshotID string
	RequiredCheck      cicontract.RequiredCheck
	Executed           bool
	Reused             bool
	ReuseProofSHA256   string
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
	AgentTokenDigest     string                   `json:"agent_token_digest"`
	Force                bool                     `json:"force"`
	AcceptedGeneration   uint64                   `json:"accepted_generation"`
	AcceptedSnapshotID   string                   `json:"accepted_snapshot_id"`
	RequiredCheck        cicontract.RequiredCheck `json:"required_check"`
	Executed             bool                     `json:"executed"`
	Reused               bool                     `json:"reused"`
	ReuseProofSHA256     string                   `json:"reuse_proof_sha256"`
	Passed               bool                     `json:"passed"`
	StartedAtUnixMilli   int64                    `json:"started_at_unix_ms"`
	CompletedAtUnixMilli int64                    `json:"completed_at_unix_ms"`
	DurationMillis       int64                    `json:"duration_ms"`
}

// CheckReceiptSHA256 计算由真实回执字段唯一决定的内容摘要。
func CheckReceiptSHA256(record CheckReceiptRecord) (string, error) {
	payload, err := json.Marshal(checkReceiptHashPayload{
		RunID: record.RunID, JobID: record.JobID, CandidateTreeSHA: record.CandidateTreeSHA, AgentTokenDigest: record.AgentTokenDigest,
		Force: record.Force, AcceptedGeneration: record.AcceptedGeneration, AcceptedSnapshotID: record.AcceptedSnapshotID,
		RequiredCheck: record.RequiredCheck, Executed: record.Executed, Reused: record.Reused, ReuseProofSHA256: record.ReuseProofSHA256, Passed: record.Passed,
		StartedAtUnixMilli: record.StartedAt.UTC().UnixMilli(), CompletedAtUnixMilli: record.CompletedAt.UTC().UnixMilli(),
		DurationMillis: record.Duration.Milliseconds(),
	})
	if err != nil {
		return "", fmt.Errorf("encode check receipt hash payload: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateCheckReceiptRecord 验证单个必跑检查回执的身份、agent digest、真实执行和内容摘要，禁止伪造 PASS。
func validateCheckReceiptRecord(record CheckReceiptRecord) error {
	if err := validateCheckReceiptIdentity(record); err != nil {
		return err
	}
	if err := validateCheckReceiptExecution(record); err != nil {
		return err
	}
	if err := validateCheckReceiptTiming(record); err != nil {
		return err
	}
	if err := validateCheckReceiptHash(record); err != nil {
		return err
	}
	if !slices.Contains(cicontract.RequiredChecks(), record.RequiredCheck) {
		return fmt.Errorf("check receipt required check %q is not canonical", record.RequiredCheck)
	}
	return nil
}

// validateCheckReceiptExecution 要求聚合回执至少覆盖 executed 或 reused，并将复用证明限制在 reused 回执上。
func validateCheckReceiptExecution(record CheckReceiptRecord) error {
	if !record.Executed && !record.Reused {
		return errors.New("check receipt must record executed=true or reused=true")
	}
	if record.Reused && !isPrefixedSHA256Digest(record.ReuseProofSHA256) {
		return errors.New("check receipt reuse proof SHA-256 is invalid")
	}
	if !record.Reused && record.ReuseProofSHA256 != "" {
		return errors.New("non-reused check receipt must not carry reuse proof SHA-256")
	}
	return nil
}

// validateCheckReceiptTiming 仅在聚合回执含 fresh execution 时接受真实毫秒级时间区间，阻断复用结果伪装为 fresh timing。
func validateCheckReceiptTiming(record CheckReceiptRecord) error {
	if !record.Executed {
		return nil
	}
	if record.StartedAt.IsZero() || record.CompletedAt.IsZero() || record.CompletedAt.Before(record.StartedAt) || record.Duration <= 0 || record.CompletedAt.Sub(record.StartedAt) != record.Duration {
		return errors.New("check receipt timing is invalid")
	}
	if !record.StartedAt.Equal(record.StartedAt.Truncate(time.Millisecond)) || !record.CompletedAt.Equal(record.CompletedAt.Truncate(time.Millisecond)) || record.Duration%time.Millisecond != 0 {
		return errors.New("check receipt timing must be millisecond precise")
	}
	return nil
}

// validateCheckReceiptHash 重算完整回执摘要，确保身份、执行方式与时间边界不能脱离 SQLite authority。
func validateCheckReceiptHash(record CheckReceiptRecord) error {
	expectedSHA256, err := CheckReceiptSHA256(record)
	if err != nil {
		return fmt.Errorf("hash check receipt: %w", err)
	}
	if record.ReceiptSHA256 != expectedSHA256 {
		return errors.New("check receipt SHA-256 does not match receipt content")
	}
	return nil
}

// validateCheckReceiptIdentity 统一校验会参与回执摘要的不可变身份，避免 agent digest 被遗漏在持久化边界之外。
func validateCheckReceiptIdentity(record CheckReceiptRecord) error {
	for field, value := range map[string]string{
		"run ID": record.RunID, "job ID": record.JobID, "candidate tree": record.CandidateTreeSHA,
		"agent token digest": record.AgentTokenDigest,
		"accepted snapshot":  record.AcceptedSnapshotID,
		"receipt SHA-256":    record.ReceiptSHA256,
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
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return fmt.Errorf("check receipt agent token digest: %w", err)
	}
	return nil
}

// validateWorkloadCatalogPassingCheckReceipts 要求回执精确覆盖持久化 workload catalog 的检查范围。
func validateWorkloadCatalogPassingCheckReceipts(catalog WorkloadCatalog, receipts []CheckReceiptRecord) error {
	required, err := RequiredChecksForWorkloadCatalog(catalog)
	if err != nil {
		return err
	}
	return validatePassingCheckReceiptsFor(required, receipts)
}

// validatePassingCheckReceiptsFor 校验同一运行回执并要求它精确覆盖给定 canonical 范围。
func validatePassingCheckReceiptsFor(required []cicontract.RequiredCheck, receipts []CheckReceiptRecord) error {
	if len(receipts) != len(required) {
		return fmt.Errorf("check receipts count = %d, want %d required checks", len(receipts), len(required))
	}
	seen, err := validatePassingCheckReceiptCollection(receipts)
	if err != nil {
		return err
	}
	for _, check := range required {
		if _, found := seen[check]; !found {
			return fmt.Errorf("required check receipt %q is missing", check)
		}
	}
	return nil
}

// validatePassingCheckReceiptCollection 校验非空回执集合的单条内容、PASS 与共享身份。
func validatePassingCheckReceiptCollection(receipts []CheckReceiptRecord) (map[cicontract.RequiredCheck]struct{}, error) {
	if len(receipts) == 0 {
		return nil, errors.New("check receipts are empty")
	}
	seen := make(map[cicontract.RequiredCheck]struct{}, len(receipts))
	var binding checkReceiptAuthorityBinding
	for index, receipt := range receipts {
		if err := binding.accept(receipt, index, seen); err != nil {
			return nil, err
		}
	}
	return seen, nil
}

// checkReceiptAuthorityBinding 保存同一 job 回执集合必须共享的 canonical SQLite 身份。
type checkReceiptAuthorityBinding struct {
	runID, jobID, tree, snapshot, agentTokenDigest string
	force                                          bool
	generation                                     uint64
}

// accept 校验一个回执并把首条回执固定为 authority binding，后续条目不得漂移。
func (binding *checkReceiptAuthorityBinding) accept(receipt CheckReceiptRecord, index int, seen map[cicontract.RequiredCheck]struct{}) error {
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
		binding.runID, binding.jobID, binding.tree = receipt.RunID, receipt.JobID, receipt.CandidateTreeSHA
		binding.generation, binding.snapshot, binding.agentTokenDigest, binding.force = receipt.AcceptedGeneration, receipt.AcceptedSnapshotID, receipt.AgentTokenDigest, receipt.Force
		return nil
	}
	if binding.matches(receipt) {
		return nil
	}
	return errors.New("check receipts do not bind one run, job, agent, tree, force mode, generation, and snapshot")
}

// matches 判断回执是否仍绑定到首条回执固定的 run、tree、generation、snapshot 与 agent digest。
func (binding checkReceiptAuthorityBinding) matches(receipt CheckReceiptRecord) bool {
	return receipt.RunID == binding.runID && receipt.JobID == binding.jobID && receipt.CandidateTreeSHA == binding.tree &&
		receipt.AgentTokenDigest == binding.agentTokenDigest && receipt.Force == binding.force && receipt.AcceptedGeneration == binding.generation &&
		receipt.AcceptedSnapshotID == binding.snapshot
}
