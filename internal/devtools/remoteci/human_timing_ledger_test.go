package remoteci

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRenderHumanTimingLedgerProjectsFinalResultInStableOrder(t *testing.T) {
	result := RunResult{
		JobID:  "job-1",
		Status: gate.ResultStatusPassed,
		CandidateTestBinaryBuilds: []CandidateTestBinaryBuilderBuild{
			{Artifact: CandidateTestBinaryArtifactRef{Package: "z/package", Mode: "race"}, Metrics: CandidateTestBinaryBuildMetrics{GoListWallMS: 1, BuildWallMS: 2, CompileActionMS: 3, LinkActionMS: 4, CompileCriticalWallMS: 5, GOCachePrivateHits: 6, GOCacheOCIProjectCacheHits: 8, GOCacheMisses: 9, GOCachePuts: 10}},
			{Artifact: CandidateTestBinaryArtifactRef{Package: "a/package", Mode: "test"}, Metrics: CandidateTestBinaryBuildMetrics{GoListWallMS: 11, BuildWallMS: 12, CompileActionMS: 13, LinkActionMS: 14, CompileCriticalWallMS: 15, GOCachePrivateHits: 16, GOCacheMisses: 17, GOCachePuts: 18}},
		},
		GateExecutions: []gate.PlanGateExecution{
			{GateID: "z-test", ExecutionProfile: gate.ExecutionProfile{TestBodyMS: 21, StartupMS: 22, TotalMS: 23}},
			{GateID: "a-test", ExecutionProfile: gate.ExecutionProfile{}},
		},
		WorkloadExecutions: []gate.PlanGateExecution{
			{GateID: "z-test:package-z", Status: gate.ResultStatusPassed, ExecutionProfile: gate.ExecutionProfile{TestBodyMS: 121, StartupMS: 122, TotalMS: 123}, TestTimings: []gate.GoTestTiming{{Name: "TestZ", DurationMS: 25, Status: gate.GoTestStatusPass}, {Name: "TestA", DurationMS: gate.FullCITargetDurationMS + 1, Status: gate.GoTestStatusPass}}},
			{GateID: "a-test:package-a", Status: gate.ResultStatusPassed, ExecutionProfile: gate.ExecutionProfile{TestBodyMS: 31, StartupMS: 32, TotalMS: 33}, TestTimings: []gate.GoTestTiming{{Name: "TestB", DurationMS: 34, Status: gate.GoTestStatusPass}}},
		},
		Shards: []ShardResult{
			{ShardIdentity: "z-shard", MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: "z-shard", Source: gate.MaterializationPhaseTiming{DownloadMS: 31, VerifyMS: 32, InstallMS: 33, MaterializeMS: 34}}},
			{ShardIdentity: "a-shard", MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: "a-shard", CandidateCLI: gate.MaterializationPhaseTiming{DownloadMS: 41, VerifyMS: 42, InstallMS: 43, MaterializeMS: 44}}},
		},
	}
	var output bytes.Buffer
	if err := RenderHumanTimingLedger(&output, result); err != nil {
		t.Fatalf("RenderHumanTimingLedger() error = %v", err)
	}
	got := output.String()
	for _, want := range []string{
		"package=a/package mode=test go_list_wall_ms=11 build_wall_ms=12 compile_action_ms=13 link_action_ms=14 compile_critical_wall_ms=15 cache_private_hits=16 cache_oci_project_cache_hits=0 cache_misses=17 cache_puts=18",
		"package=z/package mode=race go_list_wall_ms=1 build_wall_ms=2 compile_action_ms=3 link_action_ms=4 compile_critical_wall_ms=5 cache_private_hits=6 cache_oci_project_cache_hits=8 cache_misses=9 cache_puts=10",
		"test=a-test test_body_ms=0 startup_ms=0 total_ms=0",
		"test=z-test test_body_ms=21 startup_ms=22 total_ms=23",
		"workload=a-test:package-a test_body_ms=31 startup_ms=32 total_ms=33 status=passed",
		"workload=z-test:package-z test_body_ms=121 startup_ms=122 total_ms=123 status=passed",
		"workload=z-test:package-z test_name=TestA test_duration_ms=100001 status=pass",
		"REMOTE_CI_TIMING_ADVISORY workload=z-test:package-z test_name=TestA test_duration_ms=100001 target_ms=100000 action=optimize_or_split",
		"shard=a-shard artifact=candidate_cli download_ms=41 verify_ms=42 install_ms=43 materialize_ms=44",
		"shard=z-shard artifact=source download_ms=31 verify_ms=32 install_ms=33 materialize_ms=34",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("RenderHumanTimingLedger() output missing %q:\n%s", want, got)
		}
	}
	for _, pair := range [][2]string{{"package=a/package", "package=z/package"}, {"test=a-test", "test=z-test"}, {"workload=a-test:package-a", "workload=z-test:package-z"}, {"test_name=TestA", "test_name=TestZ"}, {"shard=a-shard", "shard=z-shard"}} {
		if strings.Index(got, pair[0]) >= strings.Index(got, pair[1]) {
			t.Fatalf("RenderHumanTimingLedger() output order %q before %q:\n%s", pair[0], pair[1], got)
		}
	}
	if strings.Contains(got, "test=z-test test_name=") {
		t.Fatalf("RenderHumanTimingLedger() projected test timings from parent gate:\n%s", got)
	}
}

func TestRenderHumanTimingLedgerMarksAbsentEvidenceNotMeasured(t *testing.T) {
	var output bytes.Buffer
	if err := RenderHumanTimingLedger(&output, RunResult{}); err != nil {
		t.Fatalf("RenderHumanTimingLedger() error = %v", err)
	}
	for _, want := range []string{"package=not_measured", "test=not_measured", "shard=not_measured"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("RenderHumanTimingLedger() output missing %q:\n%s", want, output.String())
		}
	}
}
