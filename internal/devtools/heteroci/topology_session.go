package heteroci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const topologyOwnerScopeDomain = "heteroci-topology-owner-scope/v1"

// TopologySession 是一次 topology owner incarnation 的 storage-neutral 身份。
// revision 不属于 session 身份；mutation store 必须在自己的事务内读取 current revision。
type TopologySession struct {
	Scope      RoutingOwnerScope
	OwnerID    RoutingOwnerID
	OwnerEpoch RoutingOwnerEpoch
}

// TopologyPlanRequest 是 topology session mutation 的 storage-neutral 原始输入。
type TopologyPlanRequest struct {
	OperationID    OperationID     `json:"operation_id"`
	RepositoryID   string          `json:"repository_id"`
	SourceTreeSHA  string          `json:"source_tree_sha"`
	RunID          string          `json:"run_id"`
	Manifest       Manifest        `json:"manifest"`
	SourceSnapshot SnapshotSet     `json:"source_snapshot"`
	Units          []ExecutionUnit `json:"units"`
	HeartbeatTTL   time.Duration   `json:"heartbeat_ttl"`
}

// TopologyReservedPlan 是 topology mutation 提交后的 storage-neutral 投影。
type TopologyReservedPlan struct {
	Fence             OwnerFence
	OperationID       OperationID
	SourceSnapshot    SnapshotSet
	EffectiveSnapshot SnapshotSet
	Plan              PackingPlan
	Decision          RoutingDecision
	Leases            []RoutingLease
}

// TopologySessionMutator 是未来 remote PG owner 实现的 topology mutation 端口。
// 实现必须事务内读取 current revision，不得用全局锁、semaphore 或本地 SQLite fallback。
type TopologySessionMutator interface {
	PlanAndReserve(ctx context.Context, session TopologySession, request TopologyPlanRequest) (TopologyReservedPlan, error)
}

// DeriveTopologyOwnerScope 只绑定 owner namespace 与 canonical physical topology。
func DeriveTopologyOwnerScope(ownerNamespace string, manifest Manifest) (RoutingOwnerScope, error) {
	if err := validateTopologyOwnerNamespace(ownerNamespace); err != nil {
		return "", err
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode heteroci topology manifest: %w", err)
	}
	material := strings.Join([]string{topologyOwnerScopeDomain, ownerNamespace, string(encoded)}, "\x00")
	digest := sha256.Sum256([]byte(material))
	return ParseRoutingOwnerScope("sha256:" + hex.EncodeToString(digest[:]))
}

// NewTopologySession 从 Acquire 返回的 fence 固定 storage-neutral owner incarnation。
func NewTopologySession(acquired OwnerFence) (TopologySession, error) {
	if err := acquired.Validate(); err != nil {
		return TopologySession{}, err
	}
	return TopologySession{Scope: acquired.Scope, OwnerID: acquired.OwnerID, OwnerEpoch: acquired.OwnerEpoch}, nil
}

// ValidateMutationFence 校验 mutation fence 属于同一 session；revision 由 storage transaction 决定。
func (session TopologySession) ValidateMutationFence(fence OwnerFence) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if err := fence.Validate(); err != nil {
		return err
	}
	if fence.Scope != session.Scope || fence.OwnerID != session.OwnerID || fence.OwnerEpoch != session.OwnerEpoch {
		return fmt.Errorf("heteroci topology mutation fence belongs to another owner incarnation")
	}
	return nil
}

// Validate 校验 topology session 的全部 storage-neutral 身份字段。
func (session TopologySession) Validate() error {
	if err := session.Scope.Validate(); err != nil {
		return err
	}
	if err := session.OwnerID.Validate(); err != nil {
		return err
	}
	if err := session.OwnerEpoch.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate 校验 topology mutation 的 target audit、snapshot 与 workload 输入。
func (request TopologyPlanRequest) Validate(session TopologySession) error {
	if err := session.Validate(); err != nil {
		return err
	}
	if _, err := ParseOperationID(string(request.OperationID)); err != nil {
		return err
	}
	if err := ValidateTopologyAuditIdentity(request.RepositoryID, request.SourceTreeSHA, request.RunID); err != nil {
		return err
	}
	if err := request.Manifest.Validate(); err != nil {
		return err
	}
	if request.SourceSnapshot.OwnerEpoch != session.OwnerEpoch {
		return fmt.Errorf("heteroci topology snapshot owner epoch does not match session")
	}
	if err := ValidateExecutionUnits(request.Units); err != nil {
		return err
	}
	if request.HeartbeatTTL <= 0 || request.HeartbeatTTL.Milliseconds() <= 0 {
		return fmt.Errorf("heteroci topology heartbeat TTL must be positive")
	}
	return nil
}

func validateTopologyOwnerNamespace(value string) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 256 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("heteroci topology owner namespace is invalid")
	}
	return nil
}

// ValidateTopologyAuditIdentity 校验每次 plan 必须显式绑定的 repository、source tree 与 run identity。
func ValidateTopologyAuditIdentity(repositoryID, sourceTree, runID string) error {
	if !validTopologyIdentity(repositoryID) || !validTopologyIdentity(runID) {
		return fmt.Errorf("heteroci topology run audit identity is invalid")
	}
	if len(sourceTree) != 40 && len(sourceTree) != 64 {
		return fmt.Errorf("heteroci topology source tree is invalid")
	}
	if _, err := hex.DecodeString(sourceTree); err != nil || strings.ToLower(sourceTree) != sourceTree {
		return fmt.Errorf("heteroci topology source tree is invalid")
	}
	return nil
}

func validTopologyIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
