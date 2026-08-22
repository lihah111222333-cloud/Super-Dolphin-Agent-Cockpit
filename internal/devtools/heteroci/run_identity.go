package heteroci

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	topologyDurableRunIdentityDomain = "heteroci-topology-run-identity/v1"
	topologyOperationIdentityDomain  = "heteroci-workflow-operation/v2"
)

// TopologyRunID 是 repository、tree 与 caller RunID 域分离派生的内部运行键。
type TopologyRunID string

// TopologyOperationPhase 是 topology authority 接受的 durable mutation 阶段。
type TopologyOperationPhase string

const (
	TopologyOperationPhasePlan          TopologyOperationPhase = "plan"
	TopologyOperationPhaseStart         TopologyOperationPhase = "start"
	TopologyOperationPhaseFinish        TopologyOperationPhase = "finish"
	TopologyOperationPhaseCancelPlanned TopologyOperationPhase = "cancel_planned"
)

// TopologyRunIdentity 将调用者可见 RunID 与跨仓唯一 durable key 绑定为一个不可拆分对象。
type TopologyRunIdentity struct {
	DurableRunID  TopologyRunID `json:"durable_run_id"`
	RepositoryID  string        `json:"repository_id"`
	SourceTreeSHA string        `json:"source_tree_sha"`
	CallerRunID   string        `json:"caller_run_id"`
}

// DeriveTopologyRunIdentity 从 canonical repository、tree 与 caller RunID 派生内部 durable key。
func DeriveTopologyRunIdentity(repositoryID, sourceTreeSHA, callerRunID string) (TopologyRunIdentity, error) {
	if err := ValidateTopologyAuditIdentity(repositoryID, sourceTreeSHA, callerRunID); err != nil {
		return TopologyRunIdentity{}, err
	}
	identity := TopologyRunIdentity{
		RepositoryID:  repositoryID,
		SourceTreeSHA: sourceTreeSHA,
		CallerRunID:   callerRunID,
		DurableRunID:  TopologyRunID(topologyIdentityDigest(topologyDurableRunIdentityDomain, repositoryID, sourceTreeSHA, callerRunID)),
	}
	return identity, nil
}

// Validate 重新派生 durable key，拒绝调用方伪造或漏绑任一 target identity 字段。
func (identity TopologyRunIdentity) Validate() error {
	expected, err := DeriveTopologyRunIdentity(identity.RepositoryID, identity.SourceTreeSHA, identity.CallerRunID)
	if err != nil {
		return err
	}
	if identity.DurableRunID != expected.DurableRunID {
		return errors.New("heteroci topology durable run identity does not match repository audit")
	}
	return nil
}

// DeriveTopologyOperationID 从内部 durable run key、阶段和 unit 派生 journal operation identity。
func DeriveTopologyOperationID(durableRunID TopologyRunID, phase TopologyOperationPhase, unitID string) (OperationID, error) {
	if err := validateTopologyDurableRunID(durableRunID); err != nil {
		return "", err
	}
	if !validTopologyOperationPhase(phase) {
		return "", errors.New("heteroci topology operation phase is invalid")
	}
	if phase == TopologyOperationPhasePlan && unitID != "" {
		return "", errors.New("heteroci topology plan operation must not carry unit")
	}
	if phase != TopologyOperationPhasePlan && !validTopologyUnitIdentity(unitID) {
		return "", errors.New("heteroci topology operation unit is invalid")
	}
	return ParseOperationID(topologyIdentityDigest(topologyOperationIdentityDomain, string(durableRunID), string(phase), unitID))
}

func validateTopologyDurableRunID(value TopologyRunID) error {
	raw := string(value)
	if len(raw) != sha256.Size*2 {
		return errors.New("heteroci topology durable run identity is invalid")
	}
	if _, err := hex.DecodeString(raw); err != nil || strings.ToLower(raw) != raw {
		return errors.New("heteroci topology durable run identity is invalid")
	}
	return nil
}

func validTopologyOperationPhase(phase TopologyOperationPhase) bool {
	switch phase {
	case TopologyOperationPhasePlan, TopologyOperationPhaseStart, TopologyOperationPhaseFinish, TopologyOperationPhaseCancelPlanned:
		return true
	default:
		return false
	}
}

func validTopologyUnitIdentity(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func topologyIdentityDigest(domain string, parts ...string) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(domain)
	for _, part := range parts {
		write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
