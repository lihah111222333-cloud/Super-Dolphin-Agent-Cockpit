package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// workloadPassEnvironmentReplayPayload 绑定一次旧环境到当前环境的 origin-tree replay。
// 旧环境摘要只用于证明来源证据；能否迁移由 remoteci 在来源树上重算 legacy/current
// worker digest 后决定，不能通过忽略 EnvironmentDigest 或 blind dual-read 放宽。
type workloadPassEnvironmentReplayPayload struct {
	Domain                   string               `json:"domain"`
	TargetIdentity           WorkloadPassIdentity `json:"target_identity"`
	SourceIdentity           WorkloadPassIdentity `json:"source_identity"`
	SourceOriginJobID        string               `json:"source_origin_job_id"`
	SourceAcceptedGeneration uint64               `json:"source_accepted_generation"`
	SourceTreeSHA            string               `json:"source_tree_sha"`
	SourceReceiptSetSHA256   string               `json:"source_receipt_set_sha256"`
	SourceEvidenceSHA256     string               `json:"source_evidence_sha256"`
}

// WorkloadPassEnvironmentReplaySHA256 把当前 identity 绑定到已验证的旧环境来源证据。
// 与普通 source replay 不同，environment digest 必须变化；输入摘要可相同或不同，
// 但调用方仍必须从 source tree 重算并比对 target InputDigest。
func WorkloadPassEnvironmentReplaySHA256(target WorkloadPassIdentity, source WorkloadPassEvidence) (string, error) {
	if err := validateWorkloadPassEnvironmentReplayPair(target, source); err != nil {
		return "", err
	}
	payload, err := json.Marshal(workloadPassEnvironmentReplayPayload{
		Domain:                   cicontract.WorkloadPassEnvironmentReplayDomain,
		TargetIdentity:           target,
		SourceIdentity:           source.Identity,
		SourceOriginJobID:        source.OriginJobID,
		SourceAcceptedGeneration: source.OriginAcceptedGeneration,
		SourceTreeSHA:            source.OriginSourceTreeSHA,
		SourceReceiptSetSHA256:   source.OriginReceiptSetSHA256,
		SourceEvidenceSHA256:     source.EvidenceSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("encode workload PASS environment replay: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func validateWorkloadPassEnvironmentReplayPair(target WorkloadPassIdentity, source WorkloadPassEvidence) error {
	if err := validateWorkloadPassIdentity(target); err != nil {
		return fmt.Errorf("validate workload PASS environment replay target: %w", err)
	}
	if err := validateWorkloadPassEvidence(source); err != nil {
		return fmt.Errorf("validate workload PASS environment replay origin: %w", err)
	}
	if target.WorkloadID != source.Identity.WorkloadID || target.ExecutionDigest != source.Identity.ExecutionDigest {
		return errors.New("workload PASS environment replay changes workload or execution identity")
	}
	if target.EnvironmentDigest == source.Identity.EnvironmentDigest {
		return errors.New("workload PASS environment replay requires a changed environment identity")
	}
	if target.IdentityDigest == source.Identity.IdentityDigest {
		return errors.New("workload PASS environment replay requires a distinct identity")
	}
	return nil
}
