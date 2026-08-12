package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

type legacyWorkloadPassIdentityPayload struct {
	WorkloadID        GateID `json:"workload_id"`
	ExecutionDigest   string `json:"execution_digest"`
	InputDigest       string `json:"input_digest"`
	EnvironmentDigest string `json:"environment_digest"`
}

var errLegacyWorkloadPassIdentityDomain = errors.New("legacy workload pass identity domain")

// legacyWorkloadPassIdentitySHA256 仅识别旧无域材料，使 retention 将其严格降为 MISS。
func legacyWorkloadPassIdentitySHA256(identity WorkloadPassIdentity) (string, error) {
	payload, err := json.Marshal(legacyWorkloadPassIdentityPayload{
		WorkloadID:        identity.WorkloadID,
		ExecutionDigest:   identity.ExecutionDigest,
		InputDigest:       identity.InputDigest,
		EnvironmentDigest: identity.EnvironmentDigest,
	})
	if err != nil {
		return "", fmt.Errorf("encode legacy workload pass identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateWorkloadPassReplayCandidateMaterial 只把旧 identity 摘要或旧 profile 可严格识别、
// 且历史原始 evidence 摘要闭合的候选标为 legacy MISS。其余漂移继续 fail-fast。
func validateWorkloadPassReplayCandidateMaterial(evidence WorkloadPassEvidence, executionJSON string) (bool, error) {
	profileErr := validateWorkloadPassReplayExecutionProfileJSON(executionJSON)
	if profileErr != nil && !errors.Is(profileErr, errLegacyRemoteCIExecutionProfile) {
		return false, profileErr
	}
	identityErr := validateWorkloadPassIdentity(evidence.Identity)
	if identityErr != nil && !errors.Is(identityErr, errLegacyWorkloadPassIdentityDomain) {
		return false, identityErr
	}
	if profileErr == nil && identityErr == nil {
		return false, validateWorkloadPassEvidence(evidence)
	}
	if err := validateWorkloadPassEvidenceOrigin(evidence); err != nil {
		return false, err
	}
	expected, err := legacyWorkloadPassEvidenceSHA256(evidence, executionJSON)
	if err != nil {
		return false, err
	}
	if evidence.EvidenceSHA256 != expected {
		return false, errors.New("workload pass evidence SHA-256 does not match content")
	}
	return true, nil
}
