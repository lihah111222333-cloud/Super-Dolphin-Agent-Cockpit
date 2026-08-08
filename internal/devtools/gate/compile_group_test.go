package gate

import (
	"strings"
	"testing"
)

func TestCompileGroupIdentitySeparatesArtifactAndSelectorGroup(t *testing.T) {
	input := CompileGroupInput{
		PackageTarget:     "./internal/devtools/gate",
		SemanticKey:       CompileGroupSemanticGoTestNormal,
		SharedInputDigest: "sha256:" + strings.Repeat("a", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("b", 64),
	}
	first := newTestCompileGroup(t, input, []GateID{"gate:test-a"})
	second := newTestCompileGroup(t, input, []GateID{"gate:test-b"})
	firstArtifact, err := CompileArtifactKey(first)
	if err != nil {
		t.Fatal(err)
	}
	secondArtifact, err := CompileArtifactKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstArtifact != secondArtifact {
		t.Fatal("selector-independent artifact key changed")
	}
	if first.GroupID == second.GroupID {
		t.Fatal("selector subgroup identities must differ")
	}
	second.ResourceClassID = "normal-large"
	second.GroupID, err = CompileGroupID(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstArtifact != secondArtifact {
		t.Fatal("resource class must not change artifact key")
	}
	if first.GroupID == second.GroupID {
		t.Fatal("resource class must bind compile group identity")
	}
}

func TestCompileGroupIdentityBindsOrderedWorkloads(t *testing.T) {
	input := CompileGroupInput{
		PackageTarget:     "./internal/devtools/gate",
		SemanticKey:       CompileGroupSemanticGoTestRace,
		SharedInputDigest: "sha256:" + strings.Repeat("c", 64),
		ProfileDigest:     "sha256:" + strings.Repeat("d", 64),
	}
	forward := newTestCompileGroup(t, input, []GateID{"gate:a", "gate:b"})
	reverse := newTestCompileGroup(t, input, []GateID{"gate:b", "gate:a"})
	if forward.GroupID == reverse.GroupID {
		t.Fatal("compile group ID must bind canonical workload order")
	}
}

func TestCanonicalShardManifestLoaderRejectsUnknownField(t *testing.T) {
	workload := mustManifestTestWorkload(t, GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary")
	group := manifestTestCompileGroup(t, []GateID{GateID(workload.ID)})
	encoded, _, err := EncodeShardExecutionManifest(manifestTestInput([]GateID{GateID(workload.ID)}, group))
	if err != nil {
		t.Fatal(err)
	}
	encoded = append([]byte(strings.TrimSuffix(string(encoded), "}")), []byte(`,"unknown":true}`)...)
	if _, err := LoadShardExecutionManifest(strings.NewReader(string(encoded))); err == nil {
		t.Fatal("strict shard execution manifest loader accepted unknown field")
	}
}

func TestCompileGroupSafetyRejectsForgedExclusivePlacement(t *testing.T) {
	codex, err := NewGoTestWorkload(GateIDBackendTestWithGuard, AtomicCodexAppPackageTarget, "TestDiscoverProcessesReturnsBothMaps", 10)
	if err != nil {
		t.Fatal(err)
	}
	codexID := GateID(codex.ID)
	exclusive := map[GateID]struct{}{codexID: {}}
	ordinary := GateID("ordinary-selector")
	for _, test := range []struct {
		name  string
		batch CompileGroupBatch
	}{
		{name: "exclusive second in ordinary batch", batch: CompileGroupBatch{BatchID: "batch-ordinary", SelectorIDs: []GateID{ordinary, codexID}}},
		{name: "exclusive first with ordinary", batch: CompileGroupBatch{BatchID: "batch-mixed", SelectorIDs: []GateID{codexID, ordinary}, Exclusive: true}},
		{name: "multiple exclusive selectors", batch: CompileGroupBatch{BatchID: "batch-multiple", SelectorIDs: []GateID{codexID, codexID}, Exclusive: true}},
		{name: "ordinary marked exclusive", batch: CompileGroupBatch{BatchID: "batch-invalid", SelectorIDs: []GateID{ordinary}, Exclusive: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateCompileGroupSafetyBatch(test.batch, exclusive); err == nil {
				t.Fatalf("forged batch unexpectedly passed: %#v", test.batch)
			}
		})
	}
	if err := validateCompileGroupSafetyBatch(CompileGroupBatch{BatchID: "batch-valid", SelectorIDs: []GateID{codexID}, Exclusive: true}, exclusive); err != nil {
		t.Fatalf("valid codex exclusive singleton rejected: %v", err)
	}
}

// TestCompileGroupSafetyRejectsHelperWithoutBatchPlan 验证旧 manifest 不能借空计划执行子进程 helper。
func TestCompileGroupSafetyRejectsHelperWithoutBatchPlan(t *testing.T) {
	helper, err := NewGoTestWorkload(
		GateIDBackendTestWithGuard,
		AtomicCodexAppPackageTarget,
		"TestCodexHelperProcess",
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	group := CompileGroup{
		PackageTarget:       AtomicCodexAppPackageTarget,
		SemanticKey:         CompileGroupSemanticGoTestNormal,
		SharedInputDigest:   "sha256:" + strings.Repeat("1", 64),
		ProfileDigest:       "sha256:" + strings.Repeat("2", 64),
		ResourceClassID:     "medium",
		WorkloadIDs:         []GateID{GateID(helper.ID)},
		CompileEstimateMS:   10,
		BodyEstimateMS:      20,
		EstimatedDurationMS: 30,
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err == nil || !strings.Contains(err.Error(), "helper process") {
		t.Fatalf("helper group validation error = %v", err)
	}
}

func TestCompileGroupBatchWaveRejectsGapsAndNonZeroStart(t *testing.T) {
	for _, test := range []struct {
		name    string
		batches []CompileGroupBatch
	}{
		{name: "non-zero start", batches: []CompileGroupBatch{{BatchID: "batch-000", Wave: 1, SelectorIDs: []GateID{"a"}}}},
		{name: "wave gap", batches: []CompileGroupBatch{{BatchID: "batch-000", Wave: 0, SelectorIDs: []GateID{"a"}}, {BatchID: "batch-001", Wave: 2, SelectorIDs: []GateID{"b"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seen := make(map[int]struct{})
			lastWave := -1
			for index, batch := range test.batches {
				if err := validateCompileGroupBatchWave(batch, seen, &lastWave); err != nil {
					if index == len(test.batches)-1 || test.name == "non-zero start" {
						return
					}
					t.Fatalf("unexpected wave validation error at batch %d: %v", index, err)
				}
			}
			t.Fatal("forged wave sequence unexpectedly passed")
		})
	}
}

func TestCompileGroupResourceClassIsExplicit(t *testing.T) {
	for _, class := range []string{"small", "medium", "maximum", "calibration"} {
		if err := validateCompileGroupResourceClass(class); err != nil {
			t.Fatalf("validateCompileGroupResourceClass(%q) = %v", class, err)
		}
		if got, err := compileGroupBatchCapacity("./internal/example", class, 3); err != nil || got != 3 {
			t.Fatalf("compileGroupBatchCapacity(%q) = %d, %v; want selector-driven capacity 3", class, got, err)
		}
	}
	for _, class := range []string{"", "normal-medium", "future"} {
		if err := validateCompileGroupResourceClass(class); err == nil {
			t.Fatalf("unknown compile group resource class %q passed identity validation", class)
		}
		if _, err := compileGroupBatchCapacity("./internal/example", class, 1); err == nil {
			t.Fatalf("unknown compile group resource class %q passed batch capacity", class)
		}
	}
}

func newTestCompileGroup(t *testing.T, input CompileGroupInput, workloadIDs []GateID) CompileGroup {
	t.Helper()
	group := CompileGroup{
		PackageTarget:       input.PackageTarget,
		SemanticKey:         input.SemanticKey,
		SharedInputDigest:   input.SharedInputDigest,
		ProfileDigest:       input.ProfileDigest,
		ResourceClassID:     "medium",
		WorkloadIDs:         workloadIDs,
		CompileEstimateMS:   10,
		BodyEstimateMS:      20,
		EstimatedDurationMS: 30,
	}
	var err error
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatal(err)
	}
	return group
}
