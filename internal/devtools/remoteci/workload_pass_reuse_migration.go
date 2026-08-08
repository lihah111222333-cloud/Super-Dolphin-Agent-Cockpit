package remoteci

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// lookupRemoteWorkloadPassesForInput 先读取当前新 identity，只有 miss 才读取
// 有界历史权威候选并执行 exact-tree 新算法重算；Force 或缺少仓库根目录时保持 MISS。
func lookupRemoteWorkloadPassesForInput(
	ctx context.Context,
	input RunInput,
	identities []gate.WorkloadPassIdentity,
) (map[string]gate.WorkloadPassEvidence, error) {
	reused, err := lookupRemoteWorkloadPasses(input.LedgerStore, identities)
	if err != nil {
		return nil, err
	}
	if input.Force || len(reused) == len(identities) || strings.TrimSpace(input.RepositoryRoot) == "" {
		return reused, nil
	}
	missing := remoteWorkloadPassMigrationMissIdentities(identities, reused)
	candidates, err := input.LedgerStore.LookupWorkloadPassEvidenceMigrationCandidates(missing)
	if err != nil {
		return nil, err
	}
	migrations := migrateRemoteWorkloadPassCandidatesWithDigestPairs(ctx, missing, candidates, newRemoteHistoricalInputDigestResolver(input).compute)
	if len(migrations) != 0 {
		persisted := make([]gate.WorkloadPassEvidenceMigration, 0, len(migrations))
		for _, migration := range migrations {
			persisted = append(persisted, gate.WorkloadPassEvidenceMigration{Source: migration.source, Projected: migration.projected})
			reused[string(migration.projected.Identity.WorkloadID)] = migration.projected
		}
		if err := input.LedgerStore.RecordMigratedWorkloadPassEvidence(persisted); err != nil {
			return nil, err
		}
	}
	return reused, nil
}

// remoteWorkloadPassMigrationMissIdentities 保持身份顺序，将当前查询 MISS
// 投影为历史迁移候选请求，绝不按 workload ID 直接授权复用。
func remoteWorkloadPassMigrationMissIdentities(
	identities []gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
) []gate.WorkloadPassIdentity {
	missing := make([]gate.WorkloadPassIdentity, 0, len(identities)-len(reused))
	for _, identity := range identities {
		if _, ok := reused[string(identity.WorkloadID)]; !ok {
			missing = append(missing, identity)
		}
	}
	return missing
}

type remoteHistoricalInputDigest func(context.Context, string, gate.GateID) (string, error)

// migrateRemoteWorkloadPassCandidates 读取历史 exact tree，并以当前新算法
// 重新计算输入摘要；只有新摘要与当前请求完全相同才在内存中回填新 identity。
func migrateRemoteWorkloadPassCandidates(
	ctx context.Context,
	input RunInput,
	identities []gate.WorkloadPassIdentity,
	candidates []gate.WorkloadPassEvidence,
) map[string]gate.WorkloadPassEvidence {
	resolver := newRemoteHistoricalInputDigestResolver(input)
	return migrateRemoteWorkloadPassCandidatesWithDigest(ctx, identities, candidates, resolver.compute)
}

type remoteHistoricalInputDigestResolver struct {
	input                RunInput
	computed             map[string]map[string]string
	errors               map[string]map[string]error
	loadedTree           map[string]bool
	snapshots            map[string]*remoteGitTreeSnapshot
	snapshotErrors       map[string]error
	currentSnapshot      *remoteGitTreeSnapshot
	currentSnapshotError error
	currentClosures      map[string][]remoteGitTreeEntry
	currentClosureErrors map[string]error
	currentClosureWhole  map[string]bool
	treeComparisons      map[string]remoteHistoricalTreeComparison
}

// remoteHistoricalTreeComparison 按历史 tree 缓存一次 raw Git path 差异。
// workload 数量很大时，负路径只查询 map，不再为每个 workload 重建整棵树。
type remoteHistoricalTreeComparison struct {
	changedPaths    map[string]struct{}
	allEntriesEqual bool
	semanticChecked bool
	semanticEqual   bool
	semanticError   error
}

// remoteWorkloadPassEvidenceMigration 保留历史 source 与当前 projected 的成对绑定，供 gate 事务持久化。
type remoteWorkloadPassEvidenceMigration struct {
	source    gate.WorkloadPassEvidence
	projected gate.WorkloadPassEvidence
}

// newRemoteHistoricalInputDigestResolver 按需缓存历史 tree 快照和单 workload
// 指纹；不会预先计算 tree×catalog 的笛卡尔积。
func newRemoteHistoricalInputDigestResolver(input RunInput) *remoteHistoricalInputDigestResolver {
	return &remoteHistoricalInputDigestResolver{
		input: input, computed: make(map[string]map[string]string), errors: make(map[string]map[string]error),
		loadedTree: make(map[string]bool), snapshots: make(map[string]*remoteGitTreeSnapshot), snapshotErrors: make(map[string]error),
		currentClosures: make(map[string][]remoteGitTreeEntry), currentClosureErrors: make(map[string]error),
		currentClosureWhole: make(map[string]bool), treeComparisons: make(map[string]remoteHistoricalTreeComparison),
	}
}

// compute resolves a workload digest from one cached historical tree; one bad
// target is retained as a target-level MISS instead of poisoning sibling targets.
func (resolver *remoteHistoricalInputDigestResolver) compute(ctx context.Context, tree string, workloadID gate.GateID) (string, error) {
	if !resolver.loadedTree[tree] {
		resolver.loadTree(ctx, tree)
	}
	if err := resolver.snapshotErrors[tree]; err != nil {
		return "", err
	}
	key := string(workloadID)
	if err := resolver.errors[tree][key]; err != nil {
		return "", err
	}
	if digest := strings.TrimSpace(resolver.computed[tree][key]); digest != "" {
		return digest, nil
	}
	if possible, err := resolver.historicalDigestMayMatchCurrentClosure(ctx, tree, workloadID); err == nil && !possible {
		mismatch := fmt.Errorf("historical workload %q observed input closure changed", workloadID)
		resolver.errors[tree][key] = mismatch
		return "", mismatch
	}
	digest, err := resolver.snapshots[tree].workloadInputDigest(ctx, gate.Workload{ID: key, Shardable: true})
	if err != nil {
		resolver.errors[tree][key] = err
		return "", err
	}
	resolver.computed[tree][key] = digest
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return "", fmt.Errorf("historical workload %q input digest is empty", workloadID)
	}
	return digest, nil
}

func (resolver *remoteHistoricalInputDigestResolver) loadTree(ctx context.Context, tree string) {
	resolver.loadedTree[tree] = true
	resolver.computed[tree] = make(map[string]string)
	resolver.errors[tree] = make(map[string]error)
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, resolver.input.RepositoryRoot, tree)
	if err != nil {
		resolver.snapshotErrors[tree] = err
		return
	}
	resolver.snapshots[tree] = snapshot
}

// historicalDigestMayMatchCurrentClosure 只做保守的源码闭包比较：发现当前算法
// 实际观察的条目在历史 tree 中缺失或 object identity 改变时，历史 digest 必然不同，
// 因而可以安全跳过完整 AST/blob 重算。无法证明不等价时返回 true，保留完整重算。
func (resolver *remoteHistoricalInputDigestResolver) historicalDigestMayMatchCurrentClosure(ctx context.Context, tree string, workloadID gate.GateID) (bool, error) {
	current, err := resolver.loadCurrentSnapshot(ctx)
	if err != nil {
		return true, err
	}
	closure, err := resolver.currentClosure(ctx, current, workloadID)
	if err != nil {
		return true, err
	}
	historical := resolver.snapshots[tree]
	if historical == nil {
		return true, errors.New("historical workload fingerprint snapshot is unavailable")
	}
	comparison := resolver.historicalTreeComparison(tree, current, historical)
	if resolver.currentClosureWhole[string(workloadID)] {
		// A whole-tree fingerprint includes every ls-tree entry. Compare the
		// entries even when object-format/tree IDs differ so an equivalent
		// candidate is never discarded during a SHA migration.
		return comparison.allEntriesEqual, nil
	}
	return resolver.historicalClosureMayMatch(ctx, tree, current, historical, closure, comparison)
}

func (resolver *remoteHistoricalInputDigestResolver) historicalClosureMayMatch(
	ctx context.Context,
	tree string,
	current *remoteGitTreeSnapshot,
	historical *remoteGitTreeSnapshot,
	closure []remoteGitTreeEntry,
	comparison remoteHistoricalTreeComparison,
) (bool, error) {
	for _, currentEntry := range closure {
		if currentEntry.path == remoteGoWorkloadSharedScriptPath {
			matches, err := resolver.historicalSemanticEntryMayMatch(ctx, tree, historical, currentEntry)
			if err != nil {
				return true, err
			}
			if !matches {
				return false, nil
			}
			continue
		}
		if _, changed := comparison.changedPaths[currentEntry.path]; changed {
			return false, nil
		}
		// The capture contract currently emits only raw tree entries plus the
		// semantic shared-script entry. Keep an explicit lookup for any future
		// derived entry so an unmodelled path remains conservative.
		if _, inCurrentTree := current.byPath[currentEntry.path]; !inCurrentTree {
			matches, err := historicalDerivedEntryMayMatch(ctx, historical, currentEntry)
			if err != nil {
				return true, err
			}
			if !matches {
				return false, nil
			}
		}
	}
	return true, nil
}

func (resolver *remoteHistoricalInputDigestResolver) historicalSemanticEntryMayMatch(
	ctx context.Context,
	tree string,
	historical *remoteGitTreeSnapshot,
	currentEntry remoteGitTreeEntry,
) (bool, error) {
	comparison := resolver.treeComparisons[tree]
	if !comparison.semanticChecked {
		historicalEntry, exists, err := historical.observedClosureEntry(ctx, currentEntry.path)
		comparison.semanticChecked = true
		comparison.semanticEqual = exists && historicalEntry == currentEntry
		comparison.semanticError = err
		resolver.treeComparisons[tree] = comparison
	}
	if comparison.semanticError != nil {
		return false, comparison.semanticError
	}
	return comparison.semanticEqual, nil
}

func historicalDerivedEntryMayMatch(
	ctx context.Context,
	historical *remoteGitTreeSnapshot,
	currentEntry remoteGitTreeEntry,
) (bool, error) {
	historicalEntry, exists, err := historical.observedClosureEntry(ctx, currentEntry.path)
	if err != nil {
		return false, err
	}
	return exists && historicalEntry == currentEntry, nil
}

func (resolver *remoteHistoricalInputDigestResolver) historicalTreeComparison(
	tree string,
	current *remoteGitTreeSnapshot,
	historical *remoteGitTreeSnapshot,
) remoteHistoricalTreeComparison {
	if resolver.treeComparisons == nil {
		resolver.treeComparisons = make(map[string]remoteHistoricalTreeComparison)
	}
	if comparison, ok := resolver.treeComparisons[tree]; ok {
		return comparison
	}
	comparison := remoteHistoricalTreeComparison{
		changedPaths:    make(map[string]struct{}),
		allEntriesEqual: remoteGitTreeEntriesEqual(current.entries, historical.entries),
	}
	for _, currentEntry := range current.entries {
		if currentEntry.path == remoteGoWorkloadSharedScriptPath {
			continue
		}
		historicalEntry, exists := historical.byPath[currentEntry.path]
		if !exists || historicalEntry != currentEntry {
			comparison.changedPaths[currentEntry.path] = struct{}{}
		}
	}
	resolver.treeComparisons[tree] = comparison
	return comparison
}

func remoteGitTreeEntriesEqual(left, right []remoteGitTreeEntry) bool {
	if len(left) != len(right) {
		return false
	}
	byPath := make(map[string]remoteGitTreeEntry, len(right))
	for _, entry := range right {
		byPath[entry.path] = entry
	}
	for _, entry := range left {
		if other, ok := byPath[entry.path]; !ok || other != entry {
			return false
		}
	}
	return true
}

func (resolver *remoteHistoricalInputDigestResolver) loadCurrentSnapshot(ctx context.Context) (*remoteGitTreeSnapshot, error) {
	if resolver.currentSnapshot != nil || resolver.currentSnapshotError != nil {
		return resolver.currentSnapshot, resolver.currentSnapshotError
	}
	if snapshot := resolver.input.workloadInputSnapshot; snapshot != nil {
		if snapshot.tree != resolver.input.Tree || snapshot.repositoryRoot != resolver.input.RepositoryRoot {
			resolver.currentSnapshotError = errors.New("prepared remote workload fingerprint snapshot identity drifted")
			return nil, resolver.currentSnapshotError
		}
		resolver.currentSnapshot = snapshot
		return resolver.currentSnapshot, nil
	}
	resolver.currentSnapshot, resolver.currentSnapshotError = loadRemoteGitTreeSnapshot(ctx, resolver.input.RepositoryRoot, resolver.input.Tree)
	return resolver.currentSnapshot, resolver.currentSnapshotError
}

func (resolver *remoteHistoricalInputDigestResolver) currentClosure(ctx context.Context, snapshot *remoteGitTreeSnapshot, workloadID gate.GateID) ([]remoteGitTreeEntry, error) {
	key := string(workloadID)
	if closure, ok := resolver.currentClosures[key]; ok {
		if resolver.currentClosureWhole == nil {
			resolver.currentClosureWhole = make(map[string]bool)
		}
		if _, known := resolver.currentClosureWhole[key]; !known {
			resolver.currentClosureWhole[key] = remoteInputClosureCoversTree(snapshot, closure)
		}
		return closure, resolver.currentClosureErrors[key]
	}
	if err, ok := resolver.currentClosureErrors[key]; ok {
		return nil, err
	}
	if closure, ok := resolver.input.workloadInputClosures[key]; ok {
		resolver.currentClosures[key] = closure
		if resolver.currentClosureWhole == nil {
			resolver.currentClosureWhole = make(map[string]bool)
		}
		resolver.currentClosureWhole[key] = remoteInputClosureCoversTree(snapshot, resolver.currentClosures[key])
		return resolver.currentClosures[key], nil
	}
	closure, err := snapshot.workloadInputClosureEntries(ctx, gate.Workload{ID: key, Shardable: true})
	if err != nil {
		resolver.currentClosureErrors[key] = err
		return nil, err
	}
	resolver.currentClosures[key] = closure
	if resolver.currentClosureWhole == nil {
		resolver.currentClosureWhole = make(map[string]bool)
	}
	resolver.currentClosureWhole[key] = remoteInputClosureCoversTree(snapshot, closure)
	return closure, nil
}

// remoteMigrationSnapshot 保留负路径所需的 exact tree entries/index，丢弃首次
// fingerprint 的 AST/blob 缓存，避免 PreparedRun 长期持有数百 MB 的源码副本。
func remoteMigrationSnapshot(snapshot *remoteGitTreeSnapshot) *remoteGitTreeSnapshot {
	if snapshot == nil {
		return nil
	}
	return &remoteGitTreeSnapshot{
		repositoryRoot:      snapshot.repositoryRoot,
		tree:                snapshot.tree,
		entries:             snapshot.entries,
		byPath:              snapshot.byPath,
		closureCaptureCalls: snapshot.closureCaptureCount(),
	}
}

// remoteInputClosureCoversTree identifies the exact full-tree fingerprint path.
func remoteInputClosureCoversTree(snapshot *remoteGitTreeSnapshot, closure []remoteGitTreeEntry) bool {
	if snapshot == nil || len(snapshot.entries) != len(closure) {
		return false
	}
	byPath := make(map[string]remoteGitTreeEntry, len(closure))
	for _, entry := range closure {
		byPath[entry.path] = entry
	}
	for _, entry := range snapshot.entries {
		if candidate, ok := byPath[entry.path]; !ok || candidate != entry {
			return false
		}
	}
	return true
}

func (snapshot *remoteGitTreeSnapshot) observedClosureEntry(ctx context.Context, filePath string) (remoteGitTreeEntry, bool, error) {
	if filePath == remoteGoWorkloadSharedScriptPath {
		selected := make(map[string]remoteGitTreeEntry, 1)
		if err := snapshot.addGoWorkloadSharedScriptEntry(ctx, selected); err != nil {
			return remoteGitTreeEntry{}, false, err
		}
		entry, ok := selected[filePath]
		return entry, ok, nil
	}
	entry, ok := snapshot.byPath[filePath]
	return entry, ok, nil
}

// migrateRemoteWorkloadPassCandidatesWithDigest 将历史候选与摘要计算器解耦，
// 供回归测试证明 unchanged/changed/failed 三类边界；生产计算器仍只调用新算法。
func migrateRemoteWorkloadPassCandidatesWithDigest(
	ctx context.Context,
	identities []gate.WorkloadPassIdentity,
	candidates []gate.WorkloadPassEvidence,
	compute remoteHistoricalInputDigest,
) map[string]gate.WorkloadPassEvidence {
	migrations := migrateRemoteWorkloadPassCandidatesWithDigestPairs(ctx, identities, candidates, compute)
	result := make(map[string]gate.WorkloadPassEvidence, len(migrations))
	for _, migration := range migrations {
		result[string(migration.projected.Identity.WorkloadID)] = migration.projected
	}
	return result
}

func migrateRemoteWorkloadPassCandidatesWithDigestPairs(
	ctx context.Context,
	identities []gate.WorkloadPassIdentity,
	candidates []gate.WorkloadPassEvidence,
	compute remoteHistoricalInputDigest,
) []remoteWorkloadPassEvidenceMigration {
	if compute == nil {
		return nil
	}
	wanted := remoteMigrationWantedIdentities(identities)
	ordered := append([]gate.WorkloadPassEvidence(nil), candidates...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return remoteMigrationEvidenceIsNewer(ordered[left], ordered[right])
	})
	result := make(map[string]remoteWorkloadPassEvidenceMigration)
	for _, candidate := range ordered {
		identity, ok := remoteMigrationCandidateIdentity(candidate, wanted)
		if !ok {
			continue
		}
		workloadKey := string(identity.WorkloadID)
		if _, exists := result[workloadKey]; exists {
			continue
		}
		if projected, ok := projectRemoteMigrationEvidence(ctx, candidate, identity, compute); ok {
			result[workloadKey] = remoteWorkloadPassEvidenceMigration{source: candidate, projected: projected}
		}
	}
	orderedResult := make([]remoteWorkloadPassEvidenceMigration, 0, len(result))
	for _, migration := range result {
		orderedResult = append(orderedResult, migration)
	}
	sort.SliceStable(orderedResult, func(left, right int) bool {
		return orderedResult[left].projected.Identity.WorkloadID < orderedResult[right].projected.Identity.WorkloadID
	})
	return orderedResult
}

// remoteMigrationEvidenceIsNewer gives equivalent projected identities a
// deterministic winner. The correctness digest mismatch path is handled by
// projectRemoteMigrationEvidence and never participates in this comparison.
func remoteMigrationEvidenceIsNewer(candidate, current gate.WorkloadPassEvidence) bool {
	if candidate.OriginAcceptedGeneration != current.OriginAcceptedGeneration {
		return candidate.OriginAcceptedGeneration > current.OriginAcceptedGeneration
	}
	if !candidate.OriginExecution.CompletedAt.Equal(current.OriginExecution.CompletedAt) {
		return candidate.OriginExecution.CompletedAt.After(current.OriginExecution.CompletedAt)
	}
	if !candidate.OriginExecution.StartedAt.Equal(current.OriginExecution.StartedAt) {
		return candidate.OriginExecution.StartedAt.After(current.OriginExecution.StartedAt)
	}
	if candidate.OriginJobID != current.OriginJobID {
		return candidate.OriginJobID > current.OriginJobID
	}
	if candidate.OriginSourceTreeSHA != current.OriginSourceTreeSHA {
		return candidate.OriginSourceTreeSHA > current.OriginSourceTreeSHA
	}
	return candidate.EvidenceSHA256 > current.EvidenceSHA256
}

func remoteMigrationWantedIdentities(identities []gate.WorkloadPassIdentity) map[gate.GateID]gate.WorkloadPassIdentity {
	wanted := make(map[gate.GateID]gate.WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		if err := identity.Validate(); err == nil {
			wanted[identity.WorkloadID] = identity
		}
	}
	return wanted
}

// remoteMigrationCandidateIdentity 严格筛选与当前执行/环境身份匹配且已验证通过的历史候选。
func remoteMigrationCandidateIdentity(candidate gate.WorkloadPassEvidence, wanted map[gate.GateID]gate.WorkloadPassIdentity) (gate.WorkloadPassIdentity, bool) {
	identity, ok := wanted[candidate.Identity.WorkloadID]
	if !ok || candidate.Identity.ExecutionDigest != identity.ExecutionDigest || candidate.Identity.EnvironmentDigest != identity.EnvironmentDigest || strings.TrimSpace(candidate.OriginSourceTreeSHA) == "" {
		return gate.WorkloadPassIdentity{}, false
	}
	if err := candidate.Validate(); err != nil || !remoteMigrationOriginExecutionPassed(candidate) {
		return gate.WorkloadPassIdentity{}, false
	}
	return identity, true
}

func projectRemoteMigrationEvidence(
	ctx context.Context,
	candidate gate.WorkloadPassEvidence,
	identity gate.WorkloadPassIdentity,
	compute remoteHistoricalInputDigest,
) (gate.WorkloadPassEvidence, bool) {
	historicalDigest, err := compute(ctx, candidate.OriginSourceTreeSHA, identity.WorkloadID)
	if err != nil || historicalDigest != identity.InputDigest {
		return gate.WorkloadPassEvidence{}, false
	}
	projected := candidate
	projected.Identity = identity
	projected.EvidenceSHA256, err = gate.WorkloadPassEvidenceSHA256(projected)
	if err != nil || projected.Validate() != nil {
		return gate.WorkloadPassEvidence{}, false
	}
	return projected, true
}

// remoteMigrationOriginExecutionPassed 防止候选即使来自坏数据库投影也绕过
// workload 级 PASS 终态检查；gate API 仍会做完整 authority/receipt 校验。
func remoteMigrationOriginExecutionPassed(evidence gate.WorkloadPassEvidence) bool {
	execution := evidence.OriginExecution
	return execution.GateID == evidence.Identity.WorkloadID && execution.Status == gate.ResultStatusPassed && execution.ExitCode == 0 && !execution.StartedAt.IsZero() && execution.CompletedAt.After(execution.StartedAt)
}
