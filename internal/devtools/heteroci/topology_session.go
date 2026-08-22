package heteroci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
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
	OperationID            OperationID         `json:"operation_id"`
	ExternalOperationID    OperationID         `json:"external_operation_id"`
	RequestDigest          string              `json:"request_digest"`
	ManifestDigest         string              `json:"manifest_digest"`
	TopologyManifestDigest string              `json:"topology_manifest_digest"`
	RunIdentity            TopologyRunIdentity `json:"run_identity"`
	Manifest               Manifest            `json:"manifest"`
	SourceSnapshot         SnapshotSet         `json:"source_snapshot"`
	Units                  []ExecutionUnit     `json:"units"`
	HeartbeatTTL           time.Duration       `json:"heartbeat_ttl"`
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

// DeriveTopologyManifestDigest 计算 owner 侧 topology manifest canonical digest。
func DeriveTopologyManifestDigest(manifest Manifest) (string, error) {
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode topology manifest digest material: %w", err)
	}
	return "sha256:" + topologyDigest("heteroci-topology-manifest/v1", string(encoded)), nil
}

// DeriveTopologyReservedPlanDigest 绑定持久化 plan、snapshot、decision 和 lease 集合。
func DeriveTopologyReservedPlanDigest(plan TopologyReservedPlan) (string, error) {
	assignments := append([]RouteAssignment(nil), plan.Decision.Assignments...)
	sort.Slice(assignments, func(left, right int) bool { return assignments[left].UnitID < assignments[right].UnitID })
	leases := append([]RoutingLease(nil), plan.Leases...)
	sort.Slice(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID })
	material := struct {
		OperationID       OperationID       `json:"operation_id"`
		SourceSnapshot    SnapshotSet       `json:"source_snapshot"`
		EffectiveSnapshot SnapshotSet       `json:"effective_snapshot"`
		Plan              PackingPlan       `json:"plan"`
		Assignments       []RouteAssignment `json:"assignments"`
		Leases            []RoutingLease    `json:"leases"`
	}{plan.OperationID, plan.SourceSnapshot, plan.EffectiveSnapshot, plan.Plan, assignments, leases}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("encode topology plan digest material: %w", err)
	}
	return "sha256:" + topologyDigest("heteroci-topology-plan/v1", string(encoded)), nil
}
func topologyDigest(domain string, parts ...string) string {
	hash := sha256.New()
	write := func(value string) {
		var length [8]byte
		length[0] = byte(len(value) >> 56)
		length[1] = byte(len(value) >> 48)
		length[2] = byte(len(value) >> 40)
		length[3] = byte(len(value) >> 32)
		length[4] = byte(len(value) >> 24)
		length[5] = byte(len(value) >> 16)
		length[6] = byte(len(value) >> 8)
		length[7] = byte(len(value))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	write(domain)
	for _, part := range parts {
		write(part)
	}
	return hex.EncodeToString(hash.Sum(nil))
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
	if err := request.validateTopologyPlanIdentity(); err != nil {
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

func (request TopologyPlanRequest) validateTopologyPlanIdentity() error {
	if _, err := ParseOperationID(string(request.OperationID)); err != nil {
		return err
	}
	if _, err := ParseOperationID(string(request.ExternalOperationID)); err != nil {
		return err
	}
	if err := request.RunIdentity.Validate(); err != nil {
		return err
	}
	if err := request.Manifest.Validate(); err != nil {
		return err
	}
	return ValidateTopologyPlanBindingAudit(request)
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

// ExecutorBindingMutation 是 request-level executor binding 的 storage-neutral durable 输入。
// owner 只接受已经由 workflow BuildBatches 生成并完成 canonical digest 的计划，不重新规划。
type ExecutorBindingMutation struct {
	Fence OwnerFence          `json:"fence"`
	Plan  ExecutorBindingPlan `json:"plan"`
}

// ExecutorBindingMutationResult 是 PG bind mutation 的 exact read-back 投影。
type ExecutorBindingMutationResult struct {
	Fence OwnerFence          `json:"fence"`
	Plan  ExecutorBindingPlan `json:"plan"`
}

// ExecutorBindingPlan 是 owner 与 workflow 之间共享的 request-level binding DTO。
// BatchIDs 使用字符串以保持 heteroci domain package 不依赖 workflow package。
type ExecutorBindingPlan struct {
	SchemaVersion          string            `json:"schema_version"`
	ExternalOperationID    OperationID       `json:"external_operation_id"`
	DurableRunID           TopologyRunID     `json:"durable_run_id"`
	RequestDigest          string            `json:"request_digest"`
	ManifestDigest         string            `json:"manifest_digest"`
	TopologyManifestDigest string            `json:"topology_manifest_digest"`
	PlanDigest             string            `json:"plan_digest"`
	AuthorityGeneration    RoutingOwnerEpoch `json:"authority_generation"`
	Bindings               []ExecutorBinding `json:"bindings"`
	BindingDigest          string            `json:"binding_digest"`
}

// ExecutorBinding 是单个 node lane 的 canonical binding 投影。
type ExecutorBinding struct {
	NodeID          string       `json:"node_id"`
	NodeIDDigest    string       `json:"node_id_digest"`
	ExecutorKind    ExecutorKind `json:"executor_kind"`
	BatchIDs        []string     `json:"batch_ids"`
	ShardDigest     string       `json:"shard_digest"`
	BatchSetDigest  string       `json:"batch_set_digest"`
	UnitSetDigest   string       `json:"unit_set_digest"`
	NodeFactsDigest string       `json:"node_facts_digest"`
}
