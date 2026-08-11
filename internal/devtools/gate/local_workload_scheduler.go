package gate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// LocalWorkloadScheduleTarget selects the execution authority before lookup.
type LocalWorkloadScheduleTarget string

const (
	LocalWorkloadTargetLocal  LocalWorkloadScheduleTarget = "local"
	LocalWorkloadTargetRemote LocalWorkloadScheduleTarget = "remote"
	LocalWorkloadTargetAuto   LocalWorkloadScheduleTarget = "auto"
	LocalWorkloadTargetHybrid LocalWorkloadScheduleTarget = "hybrid"
)

// LocalWorkloadResource describes the catalog cost used by host admission.
type LocalWorkloadResource struct {
	DurationMS int64
	CPU        float64
	MemoryGiB  float64
}

// LocalHostAdmission is a snapshot of local capacity; it is never a PASS key.
type LocalHostAdmission struct {
	Allowed               bool
	AvailableCPU          float64
	AvailableMemoryGiB    float64
	MaxDurationMS         int64
	CPUWindowStart        time.Time
	CPUWindowEnd          time.Time
	CPUSampleCount        int
	CPUBusyAveragePercent float64
}

// LocalWorkloadScheduleItem binds one canonical workload to its catalog
// admission data. A receipt-covered ineligible workload can carry a local
// identity/key so a historical local PASS is reused before host admission.
// Any explicitly ineligible workload without that sealed coverage is a
// zero-identity direct-remote item.
type LocalWorkloadScheduleItem struct {
	WorkloadID    GateID
	LocalKey      WorkloadPassKey
	LocalIdentity WorkloadPassIdentity
	Resource      LocalWorkloadResource
	LocalEligible bool
}

// LocalMaterializedTree is the only source root accepted by a local executor.
type LocalMaterializedTree struct {
	Root          string
	SourceTreeSHA string
	// Restore clears the previous workload's generated files and restores the exact source tree.
	Restore func() error
	// Verify proves the exact source tree is still clean before execution.
	Verify func() error
	// ExecutorCleanup closes one batch-local dependency/session overlay.
	ExecutorCleanup func() error
	Cleanup         func() error
}

// LocalWorkloadSchedulerInput supplies exact-tree materialization and execution.
type LocalWorkloadSchedulerInput struct {
	Target          LocalWorkloadScheduleTarget
	Items           []LocalWorkloadScheduleItem
	Host            LocalHostAdmission
	SourceTreeSHA   string
	LocalGeneration uint64
	Origin          LocalWorkloadPassOrigin
	RunID           string
	Now             func() time.Time
	SampleHost      LocalHostAdmissionSampler
	Materialize     func(context.Context, string) (LocalMaterializedTree, error)
	Execute         func(context.Context, string, GateID) (PlanGateExecution, error)
	Receipt         LocalExecutorSessionReceipt
	RemoteExecute   func(context.Context, []GateID) error
}

// LocalWorkloadScheduleStats exposes authority and executor counters for audit.
type LocalWorkloadScheduleStats struct {
	SelectedLocal     int
	SelectedRemote    int
	LocalHits         int
	LocalMisses       int
	LocalExecuted     int
	RemoteInvocations int
}

// LocalWorkloadScheduleResult is a frozen decision plus local evidence outcome.
type LocalWorkloadScheduleResult struct {
	Hits      []WorkloadPassEvidence
	Misses    []WorkloadPassIdentity
	Remote    []GateID
	Evidence  []WorkloadPassEvidence
	Stats     LocalWorkloadScheduleStats
	Admission LocalHostAdmission
}

// PrepareLocalWorkloadSchedule 先选定每个 workload 的 authority namespace，再只查本地证据；本地 MISS 不得别名到远程。
func PrepareLocalWorkloadSchedule(ctx context.Context, store *DurationLedgerStore, input LocalWorkloadSchedulerInput) (LocalWorkloadScheduleResult, error) {
	if err := validateLocalSchedulerInput(ctx, store, input); err != nil {
		return LocalWorkloadScheduleResult{}, err
	}
	localItems, remoteItems, err := classifyLocalSchedulerItems(input)
	if err != nil {
		return LocalWorkloadScheduleResult{}, err
	}
	if err := validateLocalSchedulerReceiptBindings(input.Receipt, localItems); err != nil {
		return LocalWorkloadScheduleResult{}, err
	}
	result, remoteItems, err := prepareLocalSchedulerLocalItems(ctx, store, input, localItems, remoteItems)
	if err != nil {
		return LocalWorkloadScheduleResult{}, err
	}
	result.Stats.LocalMisses = len(result.Misses)
	result.Stats.SelectedLocal = result.Stats.LocalHits + result.Stats.LocalMisses
	result.Stats.SelectedRemote = len(remoteItems)
	result.Remote = workloadIDsForLocalScheduleItems(remoteItems)
	sortLocalSchedulerMisses(result.Misses, localSchedulerItemsByDigest(localItems))
	slices.SortFunc(result.Remote, func(left, right GateID) int { return strings.Compare(string(left), string(right)) })
	return result, nil
}

// prepareLocalSchedulerLocalItems 先以本地 PASS 命中结果确定执行边界，再仅为缺失项做准入与拆分。
// 账本查询、准入或拆分失败均立即中止，禁止不完整结果越过受回执约束的调度权威。
func prepareLocalSchedulerLocalItems(ctx context.Context, store *DurationLedgerStore, input LocalWorkloadSchedulerInput, localItems, remoteItems []LocalWorkloadScheduleItem) (LocalWorkloadScheduleResult, []LocalWorkloadScheduleItem, error) {
	result := LocalWorkloadScheduleResult{Admission: input.Host}
	if len(localItems) == 0 {
		return result, remoteItems, nil
	}
	hits, misses, err := lookupSelectedLocalWorkloads(store, localItems)
	if err != nil {
		return LocalWorkloadScheduleResult{}, nil, err
	}
	result.Hits = append([]WorkloadPassEvidence(nil), hits...)
	result.Stats.LocalHits = len(hits)
	if len(misses) != 0 {
		admission, err := admitLocalSchedulerMisses(ctx, input, localItems, misses)
		if err != nil {
			return LocalWorkloadScheduleResult{}, nil, err
		}
		result.Admission = admission
		input.Host = admission
	}
	result.Misses, remoteItems, err = splitLocalSchedulerMisses(input, localItems, misses, remoteItems)
	if err != nil {
		return LocalWorkloadScheduleResult{}, nil, err
	}
	return result, remoteItems, nil
}

func admitLocalSchedulerMisses(ctx context.Context, input LocalWorkloadSchedulerInput, localItems []LocalWorkloadScheduleItem, misses []WorkloadPassIdentity) (LocalHostAdmission, error) {
	if input.Target == LocalWorkloadTargetLocal {
		if workloadID, ineligible := localSchedulerIneligibleMiss(misses, localSchedulerItemsByDigest(localItems)); ineligible {
			return LocalHostAdmission{}, fmt.Errorf("local target cannot admit workload %q; remote fallback is forbidden", workloadID)
		}
	}
	return observeLocalSchedulerHost(ctx, input)
}

// validateLocalSchedulerReceiptBindings 在 PASS 查询前核验生产者封存回执的环境与本地身份摘要。
// 任一缺失、无效或不一致均立即拒绝，防止调用方篡改权威查询键并保持全命中路径无物化副作用。
func validateLocalSchedulerReceiptBindings(receipt LocalExecutorSessionReceipt, items []LocalWorkloadScheduleItem) error {
	if len(items) == 0 {
		return nil
	}
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return fmt.Errorf("local workload scheduler requires a producer-sealed executor receipt: %w", err)
	}
	for _, item := range items {
		environment, err := receipt.EnvironmentFor(item.LocalIdentity.WorkloadID)
		if err != nil {
			return fmt.Errorf("local workload %q receipt environment: %w", item.LocalIdentity.WorkloadID, err)
		}
		if err := ValidateLocalWorkloadPassEnvironment(environment); err != nil {
			return fmt.Errorf("local workload %q receipt environment is invalid: %w", item.LocalIdentity.WorkloadID, err)
		}
		digest, err := LocalWorkloadPassEnvironmentDigest(environment)
		if err != nil {
			return fmt.Errorf("local workload %q receipt environment digest: %w", item.LocalIdentity.WorkloadID, err)
		}
		if digest != item.LocalIdentity.EnvironmentDigest {
			return fmt.Errorf("local workload %q identity environment digest does not match verified receipt", item.LocalIdentity.WorkloadID)
		}
	}
	return nil
}

// RunSelectedRemoteWorkloads 由显式执行阶段调用远程组；Prepare 阶段不会产生 ECI 副作用。
func RunSelectedRemoteWorkloads(ctx context.Context, input LocalWorkloadSchedulerInput, prepared *LocalWorkloadScheduleResult) error {
	if prepared == nil {
		return errors.New("local workload schedule result is required")
	}
	if err := executeSelectedRemoteWorkloads(ctx, input.RemoteExecute, prepared.Remote, &prepared.Stats); err != nil {
		return err
	}
	return nil
}

// RunLocalWorkloadMisses 只执行冻结的本地 MISS，并把绿色条目原子提升；本地失败绝不静默重试远程 ECI。
func RunLocalWorkloadMisses(ctx context.Context, store *DurationLedgerStore, input LocalWorkloadSchedulerInput, prepared LocalWorkloadScheduleResult) (LocalWorkloadScheduleResult, error) {
	if ctx == nil || store == nil {
		return LocalWorkloadScheduleResult{}, errors.New("local workload runner context and ledger store are required")
	}
	if err := validatePreparedLocalMisses(input, prepared); err != nil {
		return prepared, err
	}
	if len(prepared.Misses) == 0 {
		return prepared, nil
	}
	if err := validateLocalMissReceipt(input.Receipt, prepared.Misses); err != nil {
		return prepared, err
	}
	input.Host = prepared.Admission
	if err := ValidateLocalHostAdmissionObservation(input.Host); err != nil {
		return prepared, err
	}
	if input.Materialize == nil || input.Execute == nil {
		return prepared, errors.New("local workload misses require exact-tree materializer and executor")
	}
	tree, err := materializeLocalSchedulerTree(ctx, input.Materialize, input.SourceTreeSHA)
	if err != nil {
		return prepared, err
	}
	started := localSchedulerNow(input.Now)
	entries, executionErr := executeLocalMisses(ctx, input.Execute, input.Receipt, tree, prepared.Misses, &prepared.Stats)
	completed := localSchedulerNow(input.Now)
	promoteErr := promoteLocalSchedulerEntries(store, input, tree, entries, started, completed, &prepared)
	return prepared, errors.Join(executionErr, promoteErr)
}

func executeSelectedRemoteWorkloads(ctx context.Context, execute func(context.Context, []GateID) error, workloadIDs []GateID, stats *LocalWorkloadScheduleStats) error {
	if len(workloadIDs) == 0 {
		return nil
	}
	if execute == nil {
		return errors.New("remote workload selection requires an explicit remote executor")
	}
	if err := execute(ctx, workloadIDs); err != nil {
		return fmt.Errorf("execute selected remote workloads: %w", err)
	}
	stats.RemoteInvocations = 1
	return nil
}

// validatePreparedLocalMisses 校验冻结结果是完整、无重复且无 identity 漂移的分区。
func validatePreparedLocalMisses(input LocalWorkloadSchedulerInput, prepared LocalWorkloadScheduleResult) error {
	claims, err := newPreparedLocalIdentityClaims(input)
	if err != nil {
		return err
	}
	if err := claims.claimEvidence(prepared.Hits, "hit"); err != nil {
		return err
	}
	if err := claims.claimIdentities(prepared.Misses, "miss"); err != nil {
		return err
	}
	if err := claims.claimRemote(input.Target, prepared.Remote); err != nil {
		return err
	}
	return claims.complete()
}

// validateLocalMissEnvironments 在任何物化前校验全部 local MISS 的环境材料与身份绑定。
func validateLocalMissReceipt(receipt LocalExecutorSessionReceipt, misses []WorkloadPassIdentity) error {
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return fmt.Errorf("local workload misses require a producer-sealed executor receipt: %w", err)
	}
	for _, identity := range misses {
		environment, err := receipt.EnvironmentFor(identity.WorkloadID)
		if err != nil {
			return fmt.Errorf("local workload %q environment material is unavailable: %w", identity.WorkloadID, err)
		}
		if err := ValidateLocalWorkloadPassEnvironment(environment); err != nil {
			return fmt.Errorf("local workload %q environment material is invalid: %w", identity.WorkloadID, err)
		}
		digest, err := LocalWorkloadPassEnvironmentDigest(environment)
		if err != nil {
			return fmt.Errorf("local workload %q environment material: %w", identity.WorkloadID, err)
		}
		if digest != identity.EnvironmentDigest {
			return fmt.Errorf("local workload %q identity environment digest does not match material", identity.WorkloadID)
		}
	}
	return nil
}

type preparedLocalIdentityClaims struct {
	allowed       map[GateID]WorkloadPassIdentity
	selected      map[GateID]struct{}
	seenWorkloads map[GateID]string
	seenDigests   map[string]string
}

// newPreparedLocalIdentityClaims 建立 local namespace 的完整 identity 索引并拒绝摘要碰撞。
func newPreparedLocalIdentityClaims(input LocalWorkloadSchedulerInput) (preparedLocalIdentityClaims, error) {
	claims := preparedLocalIdentityClaims{
		allowed:       make(map[GateID]WorkloadPassIdentity, len(input.Items)),
		selected:      make(map[GateID]struct{}, len(input.Items)),
		seenWorkloads: make(map[GateID]string, len(input.Items)),
		seenDigests:   make(map[string]string, len(input.Items)),
	}
	for _, item := range input.Items {
		claims.selected[item.WorkloadID] = struct{}{}
		if !localSchedulerItemHasIdentity(item) {
			continue
		}
		if prior, duplicate := claims.seenDigests[item.LocalIdentity.IdentityDigest]; duplicate && prior != string(item.LocalIdentity.WorkloadID) {
			return preparedLocalIdentityClaims{}, fmt.Errorf("local workload scheduler local identity digest %q collides", item.LocalIdentity.IdentityDigest)
		}
		claims.allowed[item.LocalIdentity.WorkloadID] = item.LocalIdentity
		claims.seenDigests[item.LocalIdentity.IdentityDigest] = string(item.LocalIdentity.WorkloadID)
	}
	clear(claims.seenDigests)
	return claims, nil
}

// claimEvidence 把 authority 返回的完整 hit identity 纳入冻结分区。
func (claims *preparedLocalIdentityClaims) claimEvidence(evidence []WorkloadPassEvidence, disposition string) error {
	identities := make([]WorkloadPassIdentity, 0, len(evidence))
	for _, hit := range evidence {
		identities = append(identities, hit.Identity)
	}
	return claims.claimIdentities(identities, disposition)
}

// claimIdentities 校验每个 identity 的全部字段并拒绝 hit/miss 重叠。
func (claims *preparedLocalIdentityClaims) claimIdentities(identities []WorkloadPassIdentity, disposition string) error {
	for _, identity := range identities {
		if err := identity.Validate(); err != nil {
			return fmt.Errorf("prepared local workload %q identity is invalid: %w", identity.WorkloadID, err)
		}
		allowed, ok := claims.allowed[identity.WorkloadID]
		if !ok {
			return fmt.Errorf("prepared %s workload %q was not selected", disposition, identity.WorkloadID)
		}
		if allowed != identity {
			return fmt.Errorf("prepared %s workload %q identity drifted", disposition, identity.WorkloadID)
		}
		if prior, duplicate := claims.seenWorkloads[identity.WorkloadID]; duplicate {
			return fmt.Errorf("prepared local workload %q appears in both %s and %s", identity.WorkloadID, prior, disposition)
		}
		if prior, duplicate := claims.seenDigests[identity.IdentityDigest]; duplicate {
			return fmt.Errorf("prepared local identity digest %q appears for %s and %s", identity.IdentityDigest, prior, disposition)
		}
		claims.seenWorkloads[identity.WorkloadID] = disposition
		claims.seenDigests[identity.IdentityDigest] = disposition
	}
	return nil
}

// claimRemote 为显式远程选择建立唯一归属：有映射的负载仍须占用其本地身份，已知未映射负载直接登记。
// 目标冲突、未选中或重复归属均立即拒绝，避免同一负载或身份跨本地与远程重复执行。
func (claims *preparedLocalIdentityClaims) claimRemote(target LocalWorkloadScheduleTarget, workloadIDs []GateID) error {
	if target == LocalWorkloadTargetLocal && len(workloadIDs) != 0 {
		return errors.New("prepared remote workloads violate explicit local target")
	}
	seen := make(map[GateID]struct{}, len(workloadIDs))
	for _, workloadID := range workloadIDs {
		if workloadID == "" {
			return errors.New("prepared remote workload ID is empty")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("prepared remote workload %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
		if _, ok := claims.selected[workloadID]; !ok {
			return fmt.Errorf("prepared remote workload %q was not selected", workloadID)
		}
		identity, ok := claims.allowed[workloadID]
		if !ok {
			if prior, duplicate := claims.seenWorkloads[workloadID]; duplicate {
				return fmt.Errorf("prepared local workload %q appears in both %s and remote", workloadID, prior)
			}
			claims.seenWorkloads[workloadID] = "remote"
			continue
		}
		if err := claims.claimIdentities([]WorkloadPassIdentity{identity}, "remote"); err != nil {
			return err
		}
	}
	return nil
}

// complete 确保每个候选 workload 恰好出现在 hit、miss 或 remote 之一。
func (claims *preparedLocalIdentityClaims) complete() error {
	if len(claims.seenWorkloads) != len(claims.selected) {
		return fmt.Errorf("prepared local workload partition is incomplete: selected=%d claimed=%d", len(claims.selected), len(claims.seenWorkloads))
	}
	return nil
}

// lookupSelectedLocalWorkloads 查询 local namespace 并把完整 hit identity 与候选逐项核对。
func lookupSelectedLocalWorkloads(store *DurationLedgerStore, items []LocalWorkloadScheduleItem) ([]WorkloadPassEvidence, []WorkloadPassIdentity, error) {
	hits, err := store.LookupLocalWorkloadPassEvidence(identitiesForLocalScheduleItems(items))
	if err != nil {
		return nil, nil, err
	}
	hitByDigest := make(map[string]struct{}, len(hits))
	for _, hit := range hits {
		matched := false
		for _, item := range items {
			if item.LocalIdentity == hit.Identity {
				matched = true
				break
			}
		}
		if !matched {
			return nil, nil, fmt.Errorf("local workload PASS hit %q does not match selected identity", hit.Identity.WorkloadID)
		}
		if _, duplicate := hitByDigest[hit.Identity.IdentityDigest]; duplicate {
			return nil, nil, fmt.Errorf("local workload PASS hit %q is duplicated", hit.Identity.WorkloadID)
		}
		hitByDigest[hit.Identity.IdentityDigest] = struct{}{}
	}
	misses := make([]WorkloadPassIdentity, 0, len(items)-len(hits))
	for _, item := range items {
		if _, ok := hitByDigest[item.LocalIdentity.IdentityDigest]; !ok {
			misses = append(misses, item.LocalIdentity)
		}
	}
	sortLocalSchedulerMisses(misses, localSchedulerItemsByDigest(items))
	return hits, misses, nil
}

// sortLocalSchedulerMisses freezes aggregate-budget admission independently
// of manifest/flag order: duration, resource cost and GateID are canonical
// tie-breakers, so a permutation cannot change the local/remote split.
func sortLocalSchedulerMisses(misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem) {
	sort.SliceStable(misses, func(left, right int) bool {
		leftItem := byDigest[misses[left].IdentityDigest]
		rightItem := byDigest[misses[right].IdentityDigest]
		if leftItem.Resource.DurationMS != rightItem.Resource.DurationMS {
			return leftItem.Resource.DurationMS < rightItem.Resource.DurationMS
		}
		if leftItem.Resource.CPU != rightItem.Resource.CPU {
			return leftItem.Resource.CPU < rightItem.Resource.CPU
		}
		if leftItem.Resource.MemoryGiB != rightItem.Resource.MemoryGiB {
			return leftItem.Resource.MemoryGiB < rightItem.Resource.MemoryGiB
		}
		if misses[left].WorkloadID != misses[right].WorkloadID {
			return misses[left].WorkloadID < misses[right].WorkloadID
		}
		return misses[left].IdentityDigest < misses[right].IdentityDigest
	})
}

// materializeLocalSchedulerTree 物化并校验本地 exact tree，失败时也清理已分配资源。
func materializeLocalSchedulerTree(ctx context.Context, materialize func(context.Context, string) (LocalMaterializedTree, error), sourceTree string) (LocalMaterializedTree, error) {
	if materialize == nil {
		return LocalMaterializedTree{}, errors.New("local workload misses require exact-tree materializer")
	}
	tree, err := materialize(ctx, sourceTree)
	if err != nil {
		return LocalMaterializedTree{}, fmt.Errorf("materialize exact local source tree: %w", err)
	}
	if err := validateLocalMaterializedTree(tree, sourceTree); err != nil {
		return LocalMaterializedTree{}, errors.Join(err, cleanupLocalMaterializedTree(tree))
	}
	if tree.Cleanup == nil {
		return LocalMaterializedTree{}, errors.Join(errors.New("local materialized tree cleanup is required"), cleanupLocalMaterializedTree(tree))
	}
	if tree.Restore == nil || tree.Verify == nil {
		return LocalMaterializedTree{}, errors.Join(errors.New("local materialized tree restore and verify proofs are required"), cleanupLocalMaterializedTree(tree))
	}
	return tree, nil
}

// promoteLocalSchedulerEntries 在清理 exact tree 后把绿色执行一次性提升到本地 authority。
func promoteLocalSchedulerEntries(store *DurationLedgerStore, input LocalWorkloadSchedulerInput, tree LocalMaterializedTree, entries []LocalWorkloadPassEntry, started, completed time.Time, prepared *LocalWorkloadScheduleResult) error {
	cleanupErr := cleanupLocalMaterializedTree(tree)
	if cleanupErr != nil {
		return fmt.Errorf("local materialized tree cleanup failed; PASS promotion is forbidden: %w", cleanupErr)
	}
	if len(entries) == 0 {
		return errors.New("local workload misses produced no green execution")
	}
	origin := input.Origin
	origin.RunID = localSchedulerRunID(input.RunID, started)
	origin.LocalGeneration = input.LocalGeneration
	origin.SourceTreeSHA = input.SourceTreeSHA
	origin.CPUWindowStart = input.Host.CPUWindowStart
	origin.CPUWindowEnd = input.Host.CPUWindowEnd
	origin.CPUSampleCount = input.Host.CPUSampleCount
	origin.CPUBusyAveragePercent = input.Host.CPUBusyAveragePercent
	origin.AvailableCPU = input.Host.AvailableCPU
	origin.AvailableMemoryGiB = input.Host.AvailableMemoryGiB
	origin.Status = ResultStatusPassed
	origin.CleanupComplete = cleanupErr == nil
	origin.StartedAt = started
	origin.CompletedAt = completed
	projection, err := LocalWorkloadPassProjectionDigest(origin, entries)
	if err != nil {
		return errors.Join(cleanupErr, err)
	}
	origin.ProjectionDigest = projection
	if err := store.RecordLocalWorkloadPassBatch(LocalWorkloadPassBatch{Origin: origin, Entries: entries}); err != nil {
		return errors.Join(cleanupErr, err)
	}
	prepared.Evidence = append([]WorkloadPassEvidence(nil), prepared.Hits...)
	for _, entry := range entries {
		evidence, evidenceErr := localEvidenceForEntry(origin, entry)
		if evidenceErr != nil {
			return errors.Join(cleanupErr, evidenceErr)
		}
		prepared.Evidence = append(prepared.Evidence, evidence)
	}
	return nil
}

// cleanupLocalMaterializedTree closes the shared executor session before deleting its source root.
func cleanupLocalMaterializedTree(tree LocalMaterializedTree) error {
	var cleanupErr error
	if tree.ExecutorCleanup != nil {
		cleanupErr = errors.Join(cleanupErr, tree.ExecutorCleanup())
	}
	if tree.Cleanup == nil {
		return errors.Join(cleanupErr, errors.New("local materialized tree cleanup is required"))
	}
	return errors.Join(cleanupErr, tree.Cleanup())
}

// validateLocalSchedulerInput 拒绝缺失、重复或跨 namespace 的调度材料。
func validateLocalSchedulerInput(ctx context.Context, store *DurationLedgerStore, input LocalWorkloadSchedulerInput) error {
	if ctx == nil || store == nil {
		return errors.New("local workload scheduler context and ledger store are required")
	}
	switch input.Target {
	case LocalWorkloadTargetLocal, LocalWorkloadTargetRemote, LocalWorkloadTargetAuto, LocalWorkloadTargetHybrid:
	default:
		return fmt.Errorf("local workload scheduler target %q is invalid", input.Target)
	}
	if !validLocalSourceTreeSHA(input.SourceTreeSHA) {
		return errors.New("local workload scheduler source tree SHA is invalid")
	}
	if input.LocalGeneration == 0 {
		return errors.New("local workload scheduler local generation is required")
	}
	if len(input.Items) == 0 {
		return errors.New("local workload scheduler requires workloads")
	}
	seen := make(map[GateID]struct{}, len(input.Items))
	for _, item := range input.Items {
		if err := validateLocalSchedulerItem(item, input.Target, seen); err != nil {
			return err
		}
	}
	return nil
}

// validateLocalSchedulerItem verifies the producer-owned workload identity
// before a scheduler can select a lookup or remote path.
func validateLocalSchedulerItem(item LocalWorkloadScheduleItem, target LocalWorkloadScheduleTarget, seen map[GateID]struct{}) error {
	if err := validateLocalSchedulerItemShape(item, seen); err != nil {
		return err
	}
	eligibility, err := EvaluateLocalWorkloadExecutionEligibility(item.WorkloadID)
	if err != nil {
		return fmt.Errorf("local workload scheduler workload %q eligibility: %w", item.WorkloadID, err)
	}
	return validateLocalSchedulerItemIdentity(item, target, eligibility)
}

// validateLocalSchedulerItemShape 要求每个调度项具有唯一负载标识和正数资源需求。
// 标识或资源不合法即立即拒绝，确保后续准入和身份归属建立在确定的调度输入上。
func validateLocalSchedulerItemShape(item LocalWorkloadScheduleItem, seen map[GateID]struct{}) error {
	if item.WorkloadID == "" {
		return errors.New("local workload scheduler workload ID is required")
	}
	if _, ok := seen[item.WorkloadID]; ok {
		return fmt.Errorf("local workload scheduler workload %q is duplicated", item.WorkloadID)
	}
	seen[item.WorkloadID] = struct{}{}
	if item.Resource.DurationMS <= 0 || item.Resource.CPU <= 0 || item.Resource.MemoryGiB <= 0 {
		return fmt.Errorf("local workload scheduler resource for %q is invalid", item.WorkloadID)
	}
	return nil
}

func validateLocalSchedulerItemIdentity(item LocalWorkloadScheduleItem, target LocalWorkloadScheduleTarget, eligibility LocalWorkloadExecutionEligibility) error {
	if !localSchedulerItemHasIdentity(item) {
		return validateDirectRemoteLocalSchedulerItem(item, target, eligibility)
	}
	if err := validateLocalSchedulerIdentity(item.LocalKey, item.LocalIdentity, WorkloadPassNamespaceLocal); err != nil {
		return err
	}
	if item.LocalIdentity.WorkloadID != item.WorkloadID {
		return fmt.Errorf("local workload scheduler workload ID %q does not match local identity %q", item.WorkloadID, item.LocalIdentity.WorkloadID)
	}
	if eligibility.Eligible != item.LocalEligible {
		return fmt.Errorf("local workload scheduler workload %q eligibility drifted", item.WorkloadID)
	}
	return nil
}

// validateDirectRemoteLocalSchedulerItem only accepts an explicit local-ineligible
// zero-identity item. Eligible, unknown, and partial-identity items must never
// bypass local PASS validation by masquerading as remote work.
func validateDirectRemoteLocalSchedulerItem(item LocalWorkloadScheduleItem, target LocalWorkloadScheduleTarget, eligibility LocalWorkloadExecutionEligibility) error {
	if eligibility.Eligible || item.LocalEligible {
		return fmt.Errorf("local workload scheduler workload %q requires a local identity", item.WorkloadID)
	}
	if target == LocalWorkloadTargetLocal {
		return fmt.Errorf("local target cannot admit workload %q; remote fallback is forbidden", item.WorkloadID)
	}
	return nil
}

func localSchedulerItemHasIdentity(item LocalWorkloadScheduleItem) bool {
	return item.LocalKey != (WorkloadPassKey{}) || item.LocalIdentity != (WorkloadPassIdentity{})
}

func validateLocalSchedulerIdentity(key WorkloadPassKey, identity WorkloadPassIdentity, namespace WorkloadPassNamespace) error {
	if err := identity.Validate(); err != nil {
		return err
	}
	if key.Namespace != namespace || key.IdentityDigest != identity.IdentityDigest {
		return fmt.Errorf("%s workload scheduler key does not match identity", namespace)
	}
	return nil
}

func classifyLocalSchedulerItems(input LocalWorkloadSchedulerInput) ([]LocalWorkloadScheduleItem, []LocalWorkloadScheduleItem, error) {
	localItems := make([]LocalWorkloadScheduleItem, 0, len(input.Items))
	remoteItems := make([]LocalWorkloadScheduleItem, 0, len(input.Items))
	for _, item := range input.Items {
		if input.Target == LocalWorkloadTargetRemote || !localSchedulerItemHasIdentity(item) {
			remoteItems = append(remoteItems, item)
			continue
		}
		localItems = append(localItems, item)
	}
	return localItems, remoteItems, nil
}

func observeLocalSchedulerHost(ctx context.Context, input LocalWorkloadSchedulerInput) (LocalHostAdmission, error) {
	if input.SampleHost != nil {
		return SampleLocalHostAdmission(ctx, input.SampleHost)
	}
	if err := ValidateLocalHostAdmissionObservation(input.Host); err != nil {
		return LocalHostAdmission{}, err
	}
	return input.Host, nil
}

// splitLocalSchedulerMisses 只对 MISS 应用 host admission，命中项已在此之前保留。
func splitLocalSchedulerMisses(input LocalWorkloadSchedulerInput, localItems []LocalWorkloadScheduleItem, misses []WorkloadPassIdentity, remoteItems []LocalWorkloadScheduleItem) ([]WorkloadPassIdentity, []LocalWorkloadScheduleItem, error) {
	byDigest := localSchedulerItemsByDigest(localItems)
	orderedMisses := append([]WorkloadPassIdentity(nil), misses...)
	sortLocalSchedulerMisses(orderedMisses, byDigest)
	if input.Target == LocalWorkloadTargetLocal {
		for _, miss := range orderedMisses {
			if !localSchedulerHardAdmitted(input.Host, byDigest[miss.IdentityDigest]) {
				return nil, nil, fmt.Errorf("local target cannot admit workload %q; remote fallback is forbidden", miss.WorkloadID)
			}
		}
		return orderedMisses, remoteItems, nil
	}
	if input.Target == LocalWorkloadTargetAuto {
		localMisses, selectedRemote := splitAutoLocalSchedulerMisses(input.Host, orderedMisses, byDigest, remoteItems)
		return localMisses, selectedRemote, nil
	}
	if input.Target != LocalWorkloadTargetHybrid {
		return orderedMisses, remoteItems, nil
	}
	localMisses, selectedRemote := splitHybridLocalSchedulerMisses(input.Host, orderedMisses, byDigest, remoteItems)
	return localMisses, selectedRemote, nil
}

// splitAutoLocalSchedulerMisses 先对完整 MISS 集合做冻结规模判定；超限时整体走 remote。
func splitAutoLocalSchedulerMisses(host LocalHostAdmission, misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem, remoteItems []LocalWorkloadScheduleItem) ([]WorkloadPassIdentity, []LocalWorkloadScheduleItem) {
	if !localSchedulerAutoScaleAdmitted(host, misses, byDigest) {
		return nil, appendLocalSchedulerRemoteItems(remoteItems, misses, byDigest)
	}
	return misses, remoteItems
}

// splitHybridLocalSchedulerMisses 在 host soft budget 内按稳定排序填入 local，其余进入 remote。
func splitHybridLocalSchedulerMisses(host LocalHostAdmission, misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem, remoteItems []LocalWorkloadScheduleItem) ([]WorkloadPassIdentity, []LocalWorkloadScheduleItem) {
	localMisses := make([]WorkloadPassIdentity, 0, len(misses))
	usedDurationMS := int64(0)
	for _, miss := range misses {
		item := byDigest[miss.IdentityDigest]
		if localSchedulerHostAdmitted(host, item) && localSchedulerBudgetAllows(host, usedDurationMS, item.Resource.DurationMS) {
			localMisses = append(localMisses, miss)
			usedDurationMS += item.Resource.DurationMS
			continue
		}
		remoteItems = append(remoteItems, item)
	}
	return localMisses, remoteItems
}

// localSchedulerAutoScaleAdmitted 要求 auto MISS 整体满足 count、总时长和单项时长阈值。
func localSchedulerAutoScaleAdmitted(host LocalHostAdmission, misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem) bool {
	if len(misses) > int(cicontract.LocalAutoMissCountLimit) {
		return false
	}
	var totalDurationMS int64
	for _, miss := range misses {
		item := byDigest[miss.IdentityDigest]
		if !localSchedulerHostAdmitted(host, item) || item.Resource.DurationMS > cicontract.LocalAutoSingleDurationLimitMS {
			return false
		}
		if totalDurationMS > cicontract.LocalAutoDurationLimitMS-item.Resource.DurationMS {
			return false
		}
		totalDurationMS += item.Resource.DurationMS
	}
	return true
}

func appendLocalSchedulerRemoteItems(remoteItems []LocalWorkloadScheduleItem, misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem) []LocalWorkloadScheduleItem {
	for _, miss := range misses {
		remoteItems = append(remoteItems, byDigest[miss.IdentityDigest])
	}
	return remoteItems
}

func localSchedulerItemsByDigest(items []LocalWorkloadScheduleItem) map[string]LocalWorkloadScheduleItem {
	byDigest := make(map[string]LocalWorkloadScheduleItem, len(items))
	for _, item := range items {
		byDigest[item.LocalIdentity.IdentityDigest] = item
	}
	return byDigest
}

func localSchedulerIneligibleMiss(misses []WorkloadPassIdentity, byDigest map[string]LocalWorkloadScheduleItem) (GateID, bool) {
	for _, miss := range misses {
		item, ok := byDigest[miss.IdentityDigest]
		if !ok || !item.LocalEligible {
			return miss.WorkloadID, true
		}
	}
	return "", false
}

// localSchedulerHostAdmitted 判断当前主机是否能承担未命中的 local workload。
func localSchedulerHostAdmitted(host LocalHostAdmission, item LocalWorkloadScheduleItem) bool {
	return localSchedulerHardAdmitted(host, item) && (host.MaxDurationMS == 0 || item.Resource.DurationMS <= host.MaxDurationMS)
}

func localSchedulerHardAdmitted(host LocalHostAdmission, item LocalWorkloadScheduleItem) bool {
	return host.Allowed && localHostCPUHardAdmitted(host) && item.LocalEligible && host.AvailableCPU >= item.Resource.CPU && host.AvailableMemoryGiB >= item.Resource.MemoryGiB
}

func localSchedulerBudgetAllows(host LocalHostAdmission, usedDurationMS, durationMS int64) bool {
	if durationMS <= 0 {
		return false
	}
	if host.MaxDurationMS == 0 {
		return true
	}
	if usedDurationMS > host.MaxDurationMS-durationMS {
		return false
	}
	return true
}

func identitiesForLocalScheduleItems(items []LocalWorkloadScheduleItem) []WorkloadPassIdentity {
	identities := make([]WorkloadPassIdentity, 0, len(items))
	for _, item := range items {
		identities = append(identities, item.LocalIdentity)
	}
	return identities
}

func workloadIDsForLocalScheduleItems(items []LocalWorkloadScheduleItem) []GateID {
	workloadIDs := make([]GateID, 0, len(items))
	for _, item := range items {
		workloadIDs = append(workloadIDs, item.WorkloadID)
	}
	return workloadIDs
}

func validateLocalMaterializedTree(tree LocalMaterializedTree, expectedTree string) error {
	if strings.TrimSpace(tree.Root) == "" || !filepath.IsAbs(tree.Root) {
		return errors.New("local materialized tree root must be absolute")
	}
	if tree.SourceTreeSHA != expectedTree {
		return errors.New("local materialized tree source tree drifted")
	}
	return nil
}

// executeLocalMisses 仅向 exact-tree executor 发送冻结的 local MISS，并保留每项环境材料。
func executeLocalMisses(ctx context.Context, execute func(context.Context, string, GateID) (PlanGateExecution, error), receipt LocalExecutorSessionReceipt, tree LocalMaterializedTree, misses []WorkloadPassIdentity, stats *LocalWorkloadScheduleStats) ([]LocalWorkloadPassEntry, error) {
	if err := validateLocalExecutorSessionReceipt(receipt); err != nil {
		return nil, fmt.Errorf("local workload misses require a producer-sealed executor receipt: %w", err)
	}
	entries := make([]LocalWorkloadPassEntry, 0, len(misses))
	var runErr error
	for _, identity := range misses {
		if err := ctx.Err(); err != nil {
			return entries, err
		}
		if err := restoreAndVerifyLocalMaterializedTree(tree); err != nil {
			return nil, fmt.Errorf("restore exact source before workload %q: %w", identity.WorkloadID, err)
		}
		if err := receipt.Reverify(tree.Root); err != nil {
			return nil, fmt.Errorf("reverify executor receipt before workload %q: %w", identity.WorkloadID, err)
		}
		stats.LocalExecuted++
		execution, err := execute(ctx, tree.Root, identity.WorkloadID)
		if err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("local workload %q: %w", identity.WorkloadID, err))
			continue
		}
		entry, entryErr := localSchedulerPassEntryFromReceipt(receipt, identity, execution)
		if entryErr != nil {
			runErr = errors.Join(runErr, entryErr)
			continue
		}
		entries = append(entries, entry)
	}
	if err := restoreAndVerifyLocalMaterializedTree(tree); err != nil {
		return nil, fmt.Errorf("restore exact source after local workload batch: %w", err)
	}
	return entries, runErr
}

// localSchedulerPassEntryFromReceipt derives one promotable entry from the sealed receipt.
func localSchedulerPassEntryFromReceipt(receipt LocalExecutorSessionReceipt, identity WorkloadPassIdentity, execution PlanGateExecution) (LocalWorkloadPassEntry, error) {
	environment, err := receipt.EnvironmentFor(identity.WorkloadID)
	if err != nil {
		return LocalWorkloadPassEntry{}, fmt.Errorf("local workload %q environment material is missing: %w", identity.WorkloadID, err)
	}
	origin := LocalWorkloadPassOrigin{ToolchainClosureDigest: environment.ToolchainClosureDigest, RunnerSemanticPolicy: environment.RunnerSemanticPolicy, RunnerSemanticDigest: environment.BaseRunnerSemanticDigest}
	origin.HostContextDigest, err = LocalWorkloadPassHostContextDigest(environment)
	if err != nil {
		return LocalWorkloadPassEntry{}, fmt.Errorf("local workload %q host context: %w", identity.WorkloadID, err)
	}
	entry := LocalWorkloadPassEntry{Identity: identity, Environment: environment, Execution: execution}
	if err := validateLocalWorkloadPassEntry(origin, entry); err != nil {
		return LocalWorkloadPassEntry{}, err
	}
	return entry, nil
}

// restoreAndVerifyLocalMaterializedTree prevents source mutations from becoming hidden cross-workload inputs.
func restoreAndVerifyLocalMaterializedTree(tree LocalMaterializedTree) error {
	if tree.Restore == nil || tree.Verify == nil {
		return errors.New("local materialized tree restore and verify proofs are required")
	}
	if err := tree.Restore(); err != nil {
		return err
	}
	if err := tree.Verify(); err != nil {
		return err
	}
	return nil
}

func localSchedulerNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func localSchedulerRunID(value string, started time.Time) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fmt.Sprintf("local-run-%d", started.UnixMilli())
}

func localEvidenceForEntry(origin LocalWorkloadPassOrigin, entry LocalWorkloadPassEntry) (WorkloadPassEvidence, error) {
	evidence := WorkloadPassEvidence{Identity: entry.Identity, OriginJobID: localWorkloadPassOriginJobPrefix + origin.RunID, OriginAcceptedGeneration: origin.LocalGeneration, OriginSourceTreeSHA: origin.SourceTreeSHA, OriginReceiptSetSHA256: origin.ProjectionDigest, OriginExecution: entry.Execution}
	digest, err := WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return WorkloadPassEvidence{}, fmt.Errorf("local workload PASS evidence digest: %w", err)
	}
	evidence.EvidenceSHA256 = digest
	return evidence, nil
}
