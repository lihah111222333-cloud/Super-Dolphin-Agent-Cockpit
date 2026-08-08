package gate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCompiledSelectorBatchNormalizesElapsedBeyondEventInterval 以精确
// test2json terminal duration 收敛过长的事件完成区间。
func TestCompiledSelectorBatchNormalizesElapsedBeyondEventInterval(t *testing.T) {
	workload := mustCompileGroupBatchWorkload(t, "TestCompileGroup")
	base := time.UnixMilli(9_100_000).UTC()
	interval := compiledSelectorBatchInterval{runAt: base.Add(time.Millisecond), completedAt: base.Add(11 * time.Millisecond)}
	observation := compiledSelectorBatchObservation{started: base, log: newBoundedPlanLog(executorPlanMaxLogBytes)}
	timings := []GoTestTiming{{Name: "TestCompileGroup", Status: GoTestStatusPass, DurationMS: 7}}
	result := compiledSelectorResultWithLog(GateID(workload.ID), []string{"go", "tool", "test2json"}, observation, "TestCompileGroup", timings, interval, nil, false)
	raw := result
	raw.CompletedAt = base.Add(11 * time.Millisecond)
	raw.ExecutionProfile.TestBodyMS = 10
	raw.ExecutionProfile.TotalMS = 11
	assertCompiledSelectorBatchRawTimingRejected(t, raw)
	assertCompiledSelectorBatchNormalizedResult(t, result)
	if err := validateCompiledSelectorBatchProfile(GateID(workload.ID), result); err != nil {
		t.Fatalf("normalized selector batch profile rejected: %v", err)
	}
	assertCompiledSelectorBatchReportRoundTrip(t, result)
}

func assertCompiledSelectorBatchRawTimingRejected(t *testing.T, raw PlanGateExecution) {
	t.Helper()
	err := validateCompiledSelectorBatchProfile(raw.GateID, raw)
	if err == nil || !strings.Contains(err.Error(), "profile body does not match terminal timing") {
		t.Fatalf("unnormalized exact timing error = %v, want terminal-profile mismatch", err)
	}
}

func assertCompiledSelectorBatchNormalizedResult(t *testing.T, result PlanGateExecution) {
	t.Helper()
	if result.Status != ResultStatusPassed {
		t.Fatalf("normalized selector status = %s", result.Status)
	}
	if got := result.CompletedAt.Sub(result.StartedAt); got != 8*time.Millisecond {
		t.Fatalf("normalized selector interval = %s, want 8ms", got)
	}
	if result.ExecutionProfile.StartupMS != 1 || result.ExecutionProfile.TestBodyMS != 7 || result.ExecutionProfile.TotalMS != 8 {
		t.Fatalf("normalized selector profile = %#v", result.ExecutionProfile)
	}
	if err := ValidatePlanGateTimingEvidence(result); err != nil {
		t.Fatalf("normalized exact timing rejected: %v", err)
	}
}

func assertCompiledSelectorBatchReportRoundTrip(t *testing.T, result PlanGateExecution) {
	t.Helper()
	report := PlanExecutionReport{
		SchemaVersion:    ExecutorPlanReportSchemaVersion,
		Profile:          ProfileLocalFast,
		PlanDigest:       "sha256:" + strings.Repeat("a", 64),
		ExecutionOutcome: SuccessfulWorkerExecutionOutcome(),
		Gates:            []PlanGateExecution{result},
	}
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatalf("normalized exact timing report encode failed: %v", err)
	}
	if _, err := DecodePlanExecutionReportChunksForGateSet(chunks, []GateID{result.GateID}); err != nil {
		t.Fatalf("normalized exact timing report round-trip failed: %v", err)
	}
}

func TestCompileGroupBatchEnvironmentUsesShortIsolatedTempRoots(t *testing.T) {
	longRunRoot := filepath.Join(t.TempDir(), strings.Repeat("long-compile-group-run-root/", 12), "run")
	if err := os.MkdirAll(longRunRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	artifact := compiledGroupArtifact{
		layout:      executorLayout{runRoot: longRunRoot},
		environment: []string{"HOME=/inherited", "TMPDIR=/inherited", "GOTMPDIR=/inherited"},
	}
	environmentA, batchRootA, shortTempRootA, err := compileGroupBatchEnvironment(artifact, "batch-a")
	if err != nil {
		t.Fatalf("compile group batch environment A: %v", err)
	}
	environmentB, batchRootB, shortTempRootB, err := compileGroupBatchEnvironment(artifact, "batch-b")
	if err != nil {
		_ = cleanupCompileGroupBatchRoots(batchRootA, shortTempRootA)
		t.Fatalf("compile group batch environment B: %v", err)
	}
	tmpA, gotmpA := environmentValue(environmentA, "TMPDIR"), environmentValue(environmentA, "GOTMPDIR")
	tmpB, gotmpB := environmentValue(environmentB, "TMPDIR"), environmentValue(environmentB, "GOTMPDIR")
	assertShortCompileGroupBatchTempPaths(t, tmpA, gotmpA, tmpB, gotmpB, shortTempRootA, shortTempRootB)
	assertUniqueCompileGroupBatchTempRoots(t, tmpA, gotmpA, tmpB, gotmpB, shortTempRootA, shortTempRootB)
	assertCompileGroupBatchTempRootParents(t, shortTempRootA, shortTempRootB)
	assertCompileGroupBatchTempPathsAvoidLongRoot(t, tmpA, gotmpA, longRunRoot)
	assertCompileGroupBatchEnvironmentIsLocal(t, environmentA, batchRootA)
	assertCompileGroupBatchRuntimePathModes(t, shortTempRootA, tmpA, gotmpA, shortTempRootB, tmpB, gotmpB)
	assertCompileGroupBatchRuntimeRootsCleaned(t, batchRootA, shortTempRootA, batchRootB, shortTempRootB)
}

func environmentValue(environment []string, key string) string {
	prefix := key + "="
	for _, value := range environment {
		if result, ok := strings.CutPrefix(value, prefix); ok {
			return result
		}
	}
	return ""
}

func assertShortCompileGroupBatchTempPaths(t *testing.T, tmpA, gotmpA, tmpB, gotmpB, rootA, rootB string) {
	t.Helper()
	if filepath.Dir(tmpA) != rootA || filepath.Dir(gotmpA) != rootA || filepath.Dir(tmpB) != rootB || filepath.Dir(gotmpB) != rootB {
		t.Fatalf("short temp directories = %q, %q, %q, %q; roots = %q, %q", tmpA, gotmpA, tmpB, gotmpB, rootA, rootB)
	}
}

func assertUniqueCompileGroupBatchTempRoots(t *testing.T, tmpA, gotmpA, tmpB, gotmpB, rootA, rootB string) {
	t.Helper()
	if rootA == rootB || tmpA == tmpB || gotmpA == gotmpB {
		t.Fatalf("batch temp roots are not unique: %q/%q and %q/%q", tmpA, gotmpA, tmpB, gotmpB)
	}
}

func assertCompileGroupBatchTempRootParents(t *testing.T, rootA, rootB string) {
	t.Helper()
	if filepath.Dir(rootA) != filepath.Clean(os.TempDir()) || filepath.Dir(rootB) != filepath.Clean(os.TempDir()) {
		t.Fatalf("short temp roots escaped temp-data root: %q, %q (temp root %q)", rootA, rootB, os.TempDir())
	}
}

func assertCompileGroupBatchTempPathsAvoidLongRoot(t *testing.T, tmpA, gotmpA, longRunRoot string) {
	t.Helper()
	if strings.HasPrefix(tmpA, longRunRoot) || strings.HasPrefix(gotmpA, longRunRoot) || len(tmpA) >= len(filepath.Join(longRunRoot, "tmp")) || len(gotmpA) >= len(filepath.Join(longRunRoot, "gotmp")) {
		t.Fatalf("short temp paths still follow long run root: %q, %q", tmpA, gotmpA)
	}
}

func assertCompileGroupBatchEnvironmentIsLocal(t *testing.T, environment []string, batchRoot string) {
	t.Helper()
	if !strings.HasPrefix(environmentValue(environment, "HOME"), batchRoot+string(filepath.Separator)) || !strings.HasPrefix(environmentValue(environment, "XDG_CACHE_HOME"), batchRoot+string(filepath.Separator)) {
		t.Fatalf("HOME/XDG did not remain batch-local: HOME=%q XDG_CACHE_HOME=%q batch=%q", environmentValue(environment, "HOME"), environmentValue(environment, "XDG_CACHE_HOME"), batchRoot)
	}
}

func assertCompileGroupBatchRuntimePathModes(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat short runtime path %q: %v", path, statErr)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("short runtime path %q mode = %v, want owner-only directory", path, info.Mode())
		}
	}
}

func assertCompileGroupBatchRuntimeRootsCleaned(t *testing.T, batchRootA, shortTempRootA, batchRootB, shortTempRootB string) {
	t.Helper()
	if err := cleanupCompileGroupBatchRoots(batchRootA, shortTempRootA); err != nil {
		t.Fatalf("cleanup batch A runtime roots: %v", err)
	}
	if err := cleanupCompileGroupBatchRoots(batchRootB, shortTempRootB); err != nil {
		t.Fatalf("cleanup batch B runtime roots: %v", err)
	}
	for _, path := range []string{batchRootA, shortTempRootA, batchRootB, shortTempRootB} {
		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("runtime root %q still exists after cleanup: %v", path, statErr)
		}
	}
}
