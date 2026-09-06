package admissionproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/orchestrationcommand"
)

const (
	contentDigestDomain       = "orch.admission-proof.content/v1"
	resourceDigestDomain      = "orch.admission-proof.resource/v1"
	authorizationDigestDomain = "orch.admission-proof.authorization/v1"
	identityDigestDomain      = "orch.admission-proof.identity/v1"
	batchDigestDomain         = "orch.admission-proof.batch/v1"
	operationDigestDomain     = "orch.admission-proof.operation/v1"
)

// FinalizeContentSnapshot 计算并写回CONTENT snapshot的内部完整性摘要。
func FinalizeContentSnapshot(snapshot ContentSnapshot) (ContentSnapshot, error) {
	snapshot.SnapshotDigest = ""
	digest, err := ComputeContentSnapshotDigest(snapshot)
	if err != nil {
		return ContentSnapshot{}, err
	}
	snapshot.SnapshotDigest = digest
	return snapshot, nil
}

// ComputeContentSnapshotDigest 计算CONTENT snapshot域分离摘要并排除self-digest。
func ComputeContentSnapshotDigest(snapshot ContentSnapshot) (string, error) {
	snapshot.SnapshotDigest = ""
	return digestAdmissionProof(contentDigestDomain, snapshot)
}

// FinalizeResourceSnapshot 计算并写回RESOURCE snapshot的内部完整性摘要。
func FinalizeResourceSnapshot(snapshot ResourceSnapshot) (ResourceSnapshot, error) {
	snapshot.SnapshotDigest = ""
	digest, err := ComputeResourceSnapshotDigest(snapshot)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	snapshot.SnapshotDigest = digest
	return snapshot, nil
}

// ComputeResourceSnapshotDigest 计算RESOURCE snapshot域分离摘要并排除self-digest。
func ComputeResourceSnapshotDigest(snapshot ResourceSnapshot) (string, error) {
	snapshot.SnapshotDigest = ""
	return digestAdmissionProof(resourceDigestDomain, snapshot)
}

func computeAuthorizationDigest(authorization orchestrationcommand.AuthorizationDecision) (string, error) {
	return digestAdmissionProof(authorizationDigestDomain, authorization)
}

func computeIdentityDigest(source, target orchestrationcommand.ActiveBindingSelector) (string, error) {
	return digestAdmissionProof(identityDigestDomain, struct {
		Source orchestrationcommand.ActiveBindingSelector `json:"source"`
		Target orchestrationcommand.ActiveBindingSelector `json:"target"`
	}{Source: source, Target: target})
}

func computeOperationID(commandID string, commandVersion int64, profileDigest string) (string, error) {
	digest, err := digestAdmissionProof(operationDigestDomain, struct {
		CommandID      string `json:"command_id"`
		CommandVersion int64  `json:"command_version"`
		ProfileDigest  string `json:"profile_digest"`
	}{CommandID: commandID, CommandVersion: commandVersion, ProfileDigest: profileDigest})
	if err != nil {
		return "", err
	}
	return "custody-" + digest, nil
}

func computeBatchDigest(input batchDigestInput) (string, error) {
	return digestAdmissionProof(batchDigestDomain, input)
}

type batchDigestInput struct {
	CommandID              string `json:"command_id"`
	CommandVersion         int64  `json:"command_version"`
	CommandProfileDigest   string `json:"command_profile_digest"`
	AuthorizationDigest    string `json:"authorization_digest"`
	IdentityFenceDigest    string `json:"identity_fence_digest"`
	ContentSnapshotDigest  string `json:"content_snapshot_digest"`
	ResourceSnapshotDigest string `json:"resource_snapshot_digest"`
	IdempotencyKeyDigest   string `json:"idempotency_key_digest"`
}

func digestAdmissionProof(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{Domain: domain, Value: value})
	if err != nil {
		return "", fmt.Errorf("encode admission proof digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
