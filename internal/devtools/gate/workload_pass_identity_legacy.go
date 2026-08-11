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
