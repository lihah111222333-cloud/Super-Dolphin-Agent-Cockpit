package remoteci

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestMigrateRemoteWorkloadPassCandidatesUnchangedInputProjectsCurrentIdentity(t *testing.T) {
	current, historical := migrationIdentityPair(t, "frontend:e2e::playwright::business", "a", "b")
	candidate := migrationEvidenceFixture(t, historical, gate.ResultStatusPassed)
	computeCalls := 0
	projected := projectedRemoteWorkloadPassCandidatesForTest(
		context.Background(),
		[]gate.WorkloadPassIdentity{current},
		[]gate.WorkloadPassEvidence{candidate},
		func(context.Context, string, gate.GateID) (string, error) {
			computeCalls++
			return current.InputDigest, nil
		},
	)
	evidence, ok := projected[string(current.WorkloadID)]
	if !ok {
		t.Fatal("unchanged historical input was not migrated")
	}
	if evidence.Identity != current {
		t.Fatalf("projected identity = %#v, want %#v", evidence.Identity, current)
	}
	expected, err := gate.WorkloadPassEvidenceSHA256(evidence)
	if err != nil || evidence.EvidenceSHA256 != expected {
		t.Fatalf("projected evidence digest = %q, want %q (err=%v)", evidence.EvidenceSHA256, expected, err)
	}
	if evidence.OriginSourceTreeSHA != candidate.OriginSourceTreeSHA || evidence.OriginJobID != candidate.OriginJobID {
		t.Fatalf("projected origin changed: %#v", evidence)
	}
}

// TestMigrateRemoteWorkloadPassCandidatesFailedOriginRunKeepsPassingWorkload
// 验证 gate 已验证的“整体 failed、独立 workload PASS”证据仍可迁移；
// remote 只接受 origin execution 的 PASS，不把整体 run 失败误作 workload 失败。
func TestMigrateRemoteWorkloadPassCandidatesFailedOriginRunKeepsPassingWorkload(t *testing.T) {
	current, historical := migrationIdentityPair(t, "frontend:e2e::playwright::business", "a", "b")
	candidate := migrationEvidenceFixture(t, historical, gate.ResultStatusPassed)
	candidate.OriginJobID = "job-overall-failed-origin"
	var err error
	candidate.EvidenceSHA256, err = gate.WorkloadPassEvidenceSHA256(candidate)
	if err != nil {
		t.Fatalf("failed-origin evidence digest: %v", err)
	}
	projected := projectedRemoteWorkloadPassCandidatesForTest(
		context.Background(),
		[]gate.WorkloadPassIdentity{current},
		[]gate.WorkloadPassEvidence{candidate},
		func(context.Context, string, gate.GateID) (string, error) { return current.InputDigest, nil },
	)
	if _, ok := projected[string(current.WorkloadID)]; !ok {
		t.Fatal("passing workload from failed origin run was not migrated")
	}
}

func TestMigrateRemoteWorkloadPassCandidatesChangedInputMisses(t *testing.T) {
	current, historical := migrationIdentityPair(t, "frontend:e2e::playwright::business", "a", "b")
	candidate := migrationEvidenceFixture(t, historical, gate.ResultStatusPassed)
	projected := projectedRemoteWorkloadPassCandidatesForTest(
		context.Background(),
		[]gate.WorkloadPassIdentity{current},
		[]gate.WorkloadPassEvidence{candidate},
		func(context.Context, string, gate.GateID) (string, error) {
			return "sha256:" + strings.Repeat("c", 64), nil
		},
	)
	if len(projected) != 0 {
		t.Fatalf("changed historical input projected evidence = %#v, want MISS", projected)
	}
}

// TestHistoricalDigestNegativePathBoundsChangedWorkloadCandidates verifies that
// changed observed source entries stop before the expensive exact digest path,
// while a candidate whose observed closure is unchanged still gets a full digest
// attempt (unrelated tree changes must not discard an equivalent candidate).
func TestHistoricalDigestNegativePathBoundsChangedWorkloadCandidates(t *testing.T) {
	workloadID := gate.GateIDWhitespaceCheck
	selected := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("a", 40), path: "selected.txt"}
	unrelated := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("b", 40), path: "unrelated.txt"}
	current := &remoteGitTreeSnapshot{
		tree: "current-tree", entries: []remoteGitTreeEntry{selected, unrelated},
		byPath: map[string]remoteGitTreeEntry{selected.path: selected, unrelated.path: unrelated},
	}
	workloads := []gate.GateID{workloadID}
	for index := 0; index < 7; index++ {
		workloads = append(workloads, gate.GateID(fmt.Sprintf("synthetic-workload-%02d", index)))
	}
	currentClosures := make(map[string][]remoteGitTreeEntry, len(workloads))
	for _, id := range workloads {
		currentClosures[string(id)] = []remoteGitTreeEntry{selected}
	}
	resolver := &remoteHistoricalInputDigestResolver{
		input:                RunInput{Tree: current.tree},
		computed:             make(map[string]map[string]string),
		errors:               make(map[string]map[string]error),
		loadedTree:           make(map[string]bool),
		snapshots:            make(map[string]*remoteGitTreeSnapshot),
		snapshotErrors:       make(map[string]error),
		currentSnapshot:      current,
		currentClosures:      currentClosures,
		currentClosureErrors: make(map[string]error),
	}
	for index := 0; index < 13; index++ {
		tree := fmt.Sprintf("old-tree-%02d", index)
		changed := selected
		changed.objectID = fmt.Sprintf("%040x", index+1)
		resolver.loadedTree[tree] = true
		resolver.computed[tree] = make(map[string]string)
		resolver.errors[tree] = make(map[string]error)
		resolver.snapshots[tree] = &remoteGitTreeSnapshot{
			tree: tree, entries: []remoteGitTreeEntry{changed, unrelated},
			byPath: map[string]remoteGitTreeEntry{changed.path: changed, unrelated.path: unrelated},
		}
		for _, id := range workloads {
			if _, err := resolver.compute(context.Background(), tree, id); err == nil {
				t.Fatalf("changed candidate %q unexpectedly produced a digest for %q", tree, id)
			}
		}
		if len(resolver.computed[tree]) != 0 {
			t.Fatalf("changed candidate %q entered exact digest cache: %#v", tree, resolver.computed[tree])
		}
	}
	if got, want := len(resolver.treeComparisons), 13; got != want {
		t.Fatalf("historical tree comparison count = %d, want one comparison per tree (%d workloads)", got, want)
	}

	equivalentTree := "equivalent-tree"
	equivalentUnrelated := unrelated
	equivalentUnrelated.objectID = strings.Repeat("c", 40)
	resolver.loadedTree[equivalentTree] = true
	resolver.computed[equivalentTree] = make(map[string]string)
	resolver.errors[equivalentTree] = make(map[string]error)
	resolver.snapshots[equivalentTree] = &remoteGitTreeSnapshot{
		tree: equivalentTree, entries: []remoteGitTreeEntry{selected, equivalentUnrelated},
		byPath: map[string]remoteGitTreeEntry{selected.path: selected, equivalentUnrelated.path: equivalentUnrelated},
	}
	if _, err := resolver.compute(context.Background(), equivalentTree, workloadID); err != nil {
		t.Fatalf("equivalent candidate was incorrectly rejected: %v", err)
	}
	if len(resolver.computed[equivalentTree]) != 1 {
		t.Fatalf("equivalent candidate exact digest cache = %#v, want one computed digest", resolver.computed[equivalentTree])
	}
}

func TestHistoricalResolverReusesPreparedCurrentClosure(t *testing.T) {
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload() error = %v", err)
	}
	selected := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("a", 40), path: "selected.txt"}
	unrelated := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("b", 40), path: "unrelated.txt"}
	current := &remoteGitTreeSnapshot{
		tree: "prepared-current", entries: []remoteGitTreeEntry{selected, unrelated},
	}
	changed := selected
	changed.objectID = strings.Repeat("c", 40)
	historical := &remoteGitTreeSnapshot{
		tree: "historical", entries: []remoteGitTreeEntry{changed, unrelated},
		byPath: map[string]remoteGitTreeEntry{changed.path: changed, unrelated.path: unrelated},
	}
	resolver := &remoteHistoricalInputDigestResolver{
		input: RunInput{
			Tree:                  current.tree,
			workloadInputSnapshot: current,
			workloadInputClosures: map[string][]remoteGitTreeEntry{workload.ID: {selected}},
		},
		computed:             map[string]map[string]string{historical.tree: {}},
		errors:               map[string]map[string]error{historical.tree: {}},
		loadedTree:           map[string]bool{historical.tree: true},
		snapshots:            map[string]*remoteGitTreeSnapshot{historical.tree: historical},
		snapshotErrors:       map[string]error{},
		currentClosures:      make(map[string][]remoteGitTreeEntry),
		currentClosureErrors: make(map[string]error),
	}
	if _, err := resolver.compute(context.Background(), historical.tree, gate.GateID(workload.ID)); err == nil {
		t.Fatal("changed historical closure unexpectedly entered exact digest path")
	}
	if _, ok := resolver.currentClosures[workload.ID]; !ok {
		t.Fatal("prepared current closure was not reused")
	}
	if len(resolver.computed[historical.tree]) != 0 {
		t.Fatalf("changed historical closure exact digest cache = %#v", resolver.computed[historical.tree])
	}
}

func TestHistoricalResolverCapturesClosureOnlyForMiss(t *testing.T) {
	selected := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("a", 40), path: "selected.txt"}
	unrelated := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: strings.Repeat("b", 40), path: "unrelated.txt"}
	current := &remoteGitTreeSnapshot{
		tree: "prepared-current", entries: []remoteGitTreeEntry{selected, unrelated},
		byPath: map[string]remoteGitTreeEntry{selected.path: selected, unrelated.path: unrelated},
	}
	changed := selected
	changed.objectID = strings.Repeat("c", 40)
	historical := &remoteGitTreeSnapshot{
		tree: "historical", entries: []remoteGitTreeEntry{changed, unrelated},
		byPath: map[string]remoteGitTreeEntry{changed.path: changed, unrelated.path: unrelated},
	}
	resolver := &remoteHistoricalInputDigestResolver{
		input:                RunInput{Tree: current.tree, workloadInputSnapshot: current},
		computed:             map[string]map[string]string{historical.tree: {}},
		errors:               map[string]map[string]error{historical.tree: {}},
		loadedTree:           map[string]bool{historical.tree: true},
		snapshots:            map[string]*remoteGitTreeSnapshot{historical.tree: historical},
		snapshotErrors:       map[string]error{},
		currentClosures:      make(map[string][]remoteGitTreeEntry),
		currentClosureErrors: make(map[string]error),
	}
	if _, err := resolver.compute(context.Background(), historical.tree, gate.GateIDWhitespaceCheck); err == nil {
		t.Fatal("changed historical tree unexpectedly entered exact digest path")
	}
	if got := current.closureCaptureCount(); got != 1 {
		t.Fatalf("MISS closure captures = %d, want exactly one", got)
	}
	if len(resolver.computed[historical.tree]) != 0 {
		t.Fatalf("changed historical tree exact digest cache = %#v", resolver.computed[historical.tree])
	}
}

// TestMigrateRemoteWorkloadPassCandidatesChoosesNewestEquivalentProjection
// 验证多个历史 origin 投影到同一 current identity 时保留最新有效证据，
// 而不是把等价 PASS 全部判成歧义。
func TestMigrateRemoteWorkloadPassCandidatesChoosesNewestEquivalentProjection(t *testing.T) {
	current, historical := migrationIdentityPair(t, "frontend:e2e::playwright::business", "a", "b")
	older := migrationEvidenceFixture(t, historical, gate.ResultStatusPassed)
	older.OriginAcceptedGeneration = 1
	older.OriginJobID = "job-equivalent-older"
	older.OriginExecution.CompletedAt = older.OriginExecution.StartedAt.Add(time.Second)
	newer := older
	newer.OriginAcceptedGeneration = 2
	newer.OriginJobID = "job-equivalent-newer"
	newer.OriginExecution.CompletedAt = older.OriginExecution.CompletedAt.Add(time.Second)
	for _, candidate := range []*gate.WorkloadPassEvidence{&older, &newer} {
		var err error
		candidate.EvidenceSHA256, err = gate.WorkloadPassEvidenceSHA256(*candidate)
		if err != nil {
			t.Fatalf("equivalent candidate digest: %v", err)
		}
	}
	computeCalls := 0
	projected := projectedRemoteWorkloadPassCandidatesForTest(
		context.Background(),
		[]gate.WorkloadPassIdentity{current},
		[]gate.WorkloadPassEvidence{older, newer},
		func(context.Context, string, gate.GateID) (string, error) {
			computeCalls++
			return current.InputDigest, nil
		},
	)
	evidence, ok := projected[string(current.WorkloadID)]
	if !ok {
		t.Fatal("equivalent historical PASS was not migrated")
	}
	if evidence.OriginJobID != newer.OriginJobID || evidence.OriginAcceptedGeneration != newer.OriginAcceptedGeneration {
		t.Fatalf("equivalent projection winner = %#v, want newer origin %#v", evidence, newer)
	}
	if computeCalls != 1 {
		t.Fatalf("equivalent projection compute calls = %d, want 1", computeCalls)
	}
}

// TestMigrateRemoteWorkloadPassCandidatesFailedEvidenceMisses keeps a failed
// workload execution out of migration even when its overall origin run is retained.
func TestMigrateRemoteWorkloadPassCandidatesFailedEvidenceMisses(t *testing.T) {
	current, historical := migrationIdentityPair(t, "frontend:e2e::playwright::business", "a", "b")
	candidate := migrationEvidenceFixture(t, historical, gate.ResultStatusFailed)
	projected := projectedRemoteWorkloadPassCandidatesForTest(
		context.Background(),
		[]gate.WorkloadPassIdentity{current},
		[]gate.WorkloadPassEvidence{candidate},
		func(context.Context, string, gate.GateID) (string, error) { return current.InputDigest, nil },
	)
	if len(projected) != 0 {
		t.Fatalf("failed historical evidence projected = %#v, want MISS", projected)
	}
}

// projectedRemoteWorkloadPassCandidatesForTest keeps map-shaped assertions in tests;
// production migration consumes the source/projected pairs directly.
func projectedRemoteWorkloadPassCandidatesForTest(
	ctx context.Context,
	identities []gate.WorkloadPassIdentity,
	candidates []gate.WorkloadPassEvidence,
	compute remoteHistoricalInputDigest,
) map[string]gate.WorkloadPassEvidence {
	migrations := migrateRemoteWorkloadPassCandidatesWithDigestPairs(ctx, identities, candidates, compute)
	projected := make(map[string]gate.WorkloadPassEvidence, len(migrations))
	for _, migration := range migrations {
		projected[string(migration.projected.Identity.WorkloadID)] = migration.projected
	}
	return projected
}

func migrationIdentityPair(t *testing.T, workloadID, oldSeed, currentSeed string) (gate.WorkloadPassIdentity, gate.WorkloadPassIdentity) {
	t.Helper()
	const environment = "e"
	oldWorkload := gate.Workload{ID: workloadID, CommandDigest: strings.Repeat("d", 64), InputDigest: "sha256:" + strings.Repeat(oldSeed, 64)}
	currentWorkload := oldWorkload
	currentWorkload.InputDigest = "sha256:" + strings.Repeat(currentSeed, 64)
	oldIdentity, err := remoteWorkloadPassIdentity(oldWorkload, nil, "sha256:"+strings.Repeat(environment, 64))
	if err != nil {
		t.Fatalf("old identity: %v", err)
	}
	currentIdentity, err := remoteWorkloadPassIdentity(currentWorkload, nil, "sha256:"+strings.Repeat(environment, 64))
	if err != nil {
		t.Fatalf("current identity: %v", err)
	}
	return currentIdentity, oldIdentity
}

func migrationEvidenceFixture(t *testing.T, identity gate.WorkloadPassIdentity, status gate.ResultStatus) gate.WorkloadPassEvidence {
	t.Helper()
	started := time.Date(2026, 8, 8, 1, 2, 3, 0, time.UTC)
	execution := gate.PlanGateExecution{
		GateID: identity.WorkloadID, ShardIdentity: "sha256:" + strings.Repeat("e", 64), Status: status,
		ExitCode: 0, StartedAt: started, CompletedAt: started.Add(time.Millisecond),
		ExecutionProfile: gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 1, TestBodyMS: 2, TotalMS: 3,
		},
	}
	if status != gate.ResultStatusPassed {
		execution.ExitCode = 1
	}
	evidence := gate.WorkloadPassEvidence{
		Identity: identity, OriginJobID: "job-migration-origin", OriginAcceptedGeneration: 1,
		OriginSourceTreeSHA: strings.Repeat("f", 40), OriginReceiptSetSHA256: "sha256:" + strings.Repeat("c", 64),
		OriginExecution: execution,
	}
	var err error
	evidence.EvidenceSHA256, err = gate.WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		t.Fatalf("evidence digest: %v", err)
	}
	return evidence
}
