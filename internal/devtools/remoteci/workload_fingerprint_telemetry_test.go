package remoteci

import (
	"context"
	"crypto/sha1"
	"fmt"
	"math"
	"path"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteFingerprintTelemetry 是测试侧的复杂度观测，不是生产计费或权威账本。
// F 是 exact-tree 条目数，W 是 workload 数，C 是每个 workload 的实际 closure
// 条目数。naiveOps 对应逐 workload 全树比较的 W*F；optimizedOps 对应一次 F
// 加每个 workload 的 closure 读取 F+W*C。
type remoteFingerprintTelemetry struct {
	workloads              int
	treeEntries            int
	closureEntries         int
	uniqueClosureEntries   int
	wholeTreeWorkloads     int
	naiveOperations        int
	optimizedOperations    int
	wholeTreeRate          float64
	packageScopeRatio      float64
	singleFixtureAmplifier float64
}

func collectRemoteFingerprintTelemetry(t testing.TB, snapshot *remoteGitTreeSnapshot, workloads []gate.Workload) remoteFingerprintTelemetry {
	t.Helper()
	if snapshot == nil {
		t.Fatal("fingerprint telemetry snapshot is nil")
		return remoteFingerprintTelemetry{}
	}
	if len(workloads) == 0 {
		t.Fatal("fingerprint telemetry workloads are empty")
	}
	unique := make(map[string]struct{})
	telemetry := remoteFingerprintTelemetry{workloads: len(workloads), treeEntries: len(snapshot.entries)}
	for _, workload := range workloads {
		_, closure, err := snapshot.workloadInputDigestWithClosure(context.Background(), workload)
		if err != nil {
			t.Fatalf("workloadInputDigestWithClosure(%q): %v", workload.ID, err)
		}
		telemetry.closureEntries += len(closure)
		if remoteInputClosureCoversTree(snapshot, closure) {
			telemetry.wholeTreeWorkloads++
		}
		for _, entry := range closure {
			unique[entry.path] = struct{}{}
		}
	}
	telemetry.uniqueClosureEntries = len(unique)
	telemetry.naiveOperations = telemetry.workloads * telemetry.treeEntries
	telemetry.optimizedOperations = telemetry.treeEntries + telemetry.closureEntries
	telemetry.wholeTreeRate = float64(telemetry.wholeTreeWorkloads) / float64(telemetry.workloads)
	telemetry.packageScopeRatio = float64(telemetry.uniqueClosureEntries) / float64(telemetry.treeEntries)
	if telemetry.uniqueClosureEntries != 0 {
		telemetry.singleFixtureAmplifier = float64(telemetry.closureEntries) / float64(telemetry.uniqueClosureEntries)
	}
	return telemetry
}

// newRemoteFingerprintTelemetryFixture creates a deterministic in-memory exact-tree
// snapshot. Unrelated entries increase F without changing the package closure C.
func newRemoteFingerprintTelemetryFixture(t testing.TB, workloadCount, unrelatedCount int) (*remoteGitTreeSnapshot, []gate.Workload) {
	t.Helper()
	if workloadCount < 1 || unrelatedCount < 0 {
		t.Fatalf("invalid fixture sizes W=%d F_unrelated=%d", workloadCount, unrelatedCount)
	}
	snapshot := testExactGoTestDigestSnapshot("")
	testSource := "package fixture\n\nimport \"testing\"\n\n"
	for index := range workloadCount {
		testSource += fmt.Sprintf("func TestSelector%d(t *testing.T) {}\n", index)
	}
	replaceRemoteFingerprintTelemetryFile(t, snapshot, "fixture/target_test.go", []byte(testSource))
	for index := range unrelatedCount {
		replaceRemoteFingerprintTelemetryFile(t, snapshot, fmt.Sprintf("unrelated/file-%03d.txt", index), []byte("unrelated\n"))
	}
	workloads := make([]gate.Workload, 0, workloadCount)
	for index := range workloadCount {
		workload, err := gate.NewGoTestWorkload(
			gate.GateIDBackendTestWithGuard,
			"./fixture",
			fmt.Sprintf("TestSelector%d", index),
			100,
		)
		if err != nil {
			t.Fatalf("NewGoTestWorkload(%d): %v", index, err)
		}
		workloads = append(workloads, workload)
	}
	return snapshot, workloads
}

func replaceRemoteFingerprintTelemetryFile(t testing.TB, snapshot *remoteGitTreeSnapshot, filePath string, source []byte) {
	t.Helper()
	sum := sha1.Sum(source)
	entry := remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: fmt.Sprintf("%x", sum), path: filePath}
	snapshot.byPath[filePath] = entry
	for index, candidate := range snapshot.entries {
		if candidate.path == filePath {
			snapshot.entries[index] = entry
			if path.Ext(filePath) == ".go" {
				snapshot.goSources[filePath] = source
			}
			return
		}
	}
	snapshot.entries = append(snapshot.entries, entry)
	if path.Ext(filePath) == ".go" {
		snapshot.goSources[filePath] = source
	}
}

// TestRemoteFingerprintTelemetryQuantifiesClosureComplexity verifies that repeated
// selectors share one package-scoped closure and do not fall back to whole-tree input.
func TestRemoteFingerprintTelemetryQuantifiesClosureComplexity(t *testing.T) {
	const workloadCount = 8
	snapshot, workloads := newRemoteFingerprintTelemetryFixture(t, workloadCount, 64)
	first := collectRemoteFingerprintTelemetry(t, snapshot, workloads)
	second := collectRemoteFingerprintTelemetry(t, snapshot, workloads)
	if first != second {
		t.Fatalf("fingerprint telemetry is not deterministic: first=%+v second=%+v", first, second)
	}
	if first.wholeTreeRate != 0 {
		t.Fatalf("whole-tree rate = %.3f, want 0 for static package selectors", first.wholeTreeRate)
	}
	if first.packageScopeRatio >= 0.5 {
		t.Fatalf("package scope ratio = %.3f, want < 0.5 of the tree", first.packageScopeRatio)
	}
	if first.singleFixtureAmplifier < float64(workloadCount)-0.01 {
		t.Fatalf("single-fixture amplification = %.3f, want approximately W=%d", first.singleFixtureAmplifier, workloadCount)
	}
	if first.optimizedOperations >= first.naiveOperations {
		t.Fatalf("optimized operation estimate = %d, naive W*F = %d", first.optimizedOperations, first.naiveOperations)
	}
	t.Logf("fingerprint telemetry W=%d F=%d C_unique=%d closure_total=%d whole_tree_rate=%.3f package_scope=%.3f single_fixture_amplification=%.3f naive_ops=%d optimized_ops=%d", first.workloads, first.treeEntries, first.uniqueClosureEntries, first.closureEntries, first.wholeTreeRate, first.packageScopeRatio, first.singleFixtureAmplifier, first.naiveOperations, first.optimizedOperations)
}

// TestRemoteFingerprintTelemetryScalesTreeOnce makes the F term observable: adding
// unrelated tree entries increases optimized work once, not once per workload.
func TestRemoteFingerprintTelemetryScalesTreeOnce(t *testing.T) {
	const workloadCount = 6
	smallSnapshot, smallWorkloads := newRemoteFingerprintTelemetryFixture(t, workloadCount, 4)
	largeSnapshot, largeWorkloads := newRemoteFingerprintTelemetryFixture(t, workloadCount, 44)
	small := collectRemoteFingerprintTelemetry(t, smallSnapshot, smallWorkloads)
	large := collectRemoteFingerprintTelemetry(t, largeSnapshot, largeWorkloads)
	treeDelta := large.treeEntries - small.treeEntries
	if treeDelta <= 0 {
		t.Fatalf("tree delta = %d, want positive", treeDelta)
	}
	if got, want := large.naiveOperations-small.naiveOperations, workloadCount*treeDelta; got != want {
		t.Fatalf("naive delta = %d, want W*deltaF = %d", got, want)
	}
	if got, want := large.optimizedOperations-small.optimizedOperations, treeDelta; got != want {
		t.Fatalf("optimized delta = %d, want deltaF = %d (one tree scan)", got, want)
	}
	if large.closureEntries != small.closureEntries {
		t.Fatalf("package closure changed after unrelated tree growth: small=%d large=%d", small.closureEntries, large.closureEntries)
	}
	t.Logf("complexity scaling W=%d deltaF=%d naive_delta=%d optimized_delta=%d", workloadCount, treeDelta, large.naiveOperations-small.naiveOperations, large.optimizedOperations-small.optimizedOperations)
}

// BenchmarkRemoteFingerprintTelemetry reports deterministic complexity metrics
// while timing the exact-tree closure path on fixture-only snapshots.
func BenchmarkRemoteFingerprintTelemetry(b *testing.B) {
	for _, config := range []struct {
		name           string
		workloadCount  int
		unrelatedCount int
	}{
		{name: "W01_F16", workloadCount: 1, unrelatedCount: 16},
		{name: "W08_F64", workloadCount: 8, unrelatedCount: 64},
		{name: "W32_F256", workloadCount: 32, unrelatedCount: 256},
	} {
		b.Run(config.name, func(b *testing.B) {
			snapshot, workloads := newRemoteFingerprintTelemetryFixture(b, config.workloadCount, config.unrelatedCount)
			telemetry := collectRemoteFingerprintTelemetryBenchmark(b, snapshot, workloads)
			b.ResetTimer()
			for b.Loop() {
				freshSnapshot, freshWorkloads := newRemoteFingerprintTelemetryFixture(b, config.workloadCount, config.unrelatedCount)
				_ = collectRemoteFingerprintTelemetryBenchmark(b, freshSnapshot, freshWorkloads)
			}
			b.StopTimer()
			b.ReportMetric(float64(telemetry.treeEntries), "tree-entries")
			b.ReportMetric(float64(telemetry.workloads), "workloads")
			b.ReportMetric(float64(telemetry.uniqueClosureEntries), "unique-closure-entries")
			b.ReportMetric(telemetry.wholeTreeRate, "whole-tree-rate")
			b.ReportMetric(telemetry.packageScopeRatio, "package-scope-ratio")
			b.ReportMetric(telemetry.singleFixtureAmplifier, "single-fixture-amplification")
			b.ReportMetric(float64(telemetry.naiveOperations), "naive-WxF-ops")
			b.ReportMetric(float64(telemetry.optimizedOperations), "optimized-FplusWxC-ops")
			b.ReportMetric(float64(telemetry.naiveOperations)/math.Max(float64(telemetry.optimizedOperations), 1), "complexity-reduction")
		})
	}
}

func collectRemoteFingerprintTelemetryBenchmark(b *testing.B, snapshot *remoteGitTreeSnapshot, workloads []gate.Workload) remoteFingerprintTelemetry {
	b.Helper()
	return collectRemoteFingerprintTelemetry(b, snapshot, workloads)
}
