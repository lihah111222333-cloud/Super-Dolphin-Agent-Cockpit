package gate

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCandidateTestBinaryReceiptBindingDigestBindsOCICacheMetricsAndTimings(t *testing.T) {
	build := candidateTestBinaryBuildRecordForBindingTest()
	first, err := CandidateTestBinaryReceiptBindingDigest([]CandidateTestBinaryBuildRecord{build}, build.CandidateTree)
	if err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*CandidateTestBinaryBuildRecord){
		"go_list_wall_time": func(record *CandidateTestBinaryBuildRecord) { record.GoListWallMS++ },
		"build_wall_time":   func(record *CandidateTestBinaryBuildRecord) { record.BuildWallMS++ },
		"compile_action_time": func(record *CandidateTestBinaryBuildRecord) {
			record.CompileActionMS++
		},
		"link_action_time": func(record *CandidateTestBinaryBuildRecord) { record.LinkActionMS++ },
		"compile_critical_wall_time": func(record *CandidateTestBinaryBuildRecord) {
			record.CompileCriticalWallMS++
		},
		"private_identity": func(record *CandidateTestBinaryBuildRecord) {
			record.GOCachePrivateRootIdentity = "sha256:" + strings.Repeat("a", 64)
		},
		"oci_baseline_hits": func(record *CandidateTestBinaryBuildRecord) { record.GOCacheOCIProjectCacheHits++ },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := candidateTestBinaryBuildRecordForBindingTest()
			mutate(&candidate)
			digest, err := CandidateTestBinaryReceiptBindingDigest([]CandidateTestBinaryBuildRecord{candidate}, candidate.CandidateTree)
			if err != nil || digest == first {
				t.Fatalf("metric tamper must change digest: digest=%q err=%v", digest, err)
			}
		})
	}
}

func TestCandidateTestBinaryBuildRecordRejectsNonBaselineToolchains(t *testing.T) {
	for _, toolchain := range []string{"go1.25.7", "go1.26.4", "go1.26.6"} {
		t.Run(toolchain, func(t *testing.T) {
			build := candidateTestBinaryBuildRecordForBindingTest()
			build.GoToolchain = toolchain
			if err := validateCandidateTestBinaryBuildRecord(build); err == nil {
				t.Fatalf("validateCandidateTestBinaryBuildRecord() accepted non-baseline toolchain %q", toolchain)
			}
		})
	}
}

func TestCandidateTestBinaryCacheMetricsSQLiteRoundTrip(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC()
	record := RemoteCIRunRecord{JobID: "cache-metrics-round-trip", Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast, PlanDigest: "sha256:plan", CatalogDigest: "sha256:catalog", SourceTreeSHA: strings.Repeat("a", 40), RunnerImage: "runner", Status: ResultStatusFailed, StartedAt: now, CompletedAt: now}
	record.CandidateTestBinaryBuilds = []CandidateTestBinaryBuildRecord{candidateTestBinaryBuildRecordForBindingTest()}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := upsertSQLiteRemoteCIRun(transaction, record); err != nil {
		t.Fatal(err)
	}
	if err := replaceCandidateTestBinaryBuilds(transaction, record); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCandidateTestBinaryBuildRows(database, record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, record.CandidateTestBinaryBuilds) {
		t.Fatalf("cache metrics round trip = %#v, want %#v", loaded, record.CandidateTestBinaryBuilds)
	}
}

func candidateTestBinaryBuildRecordForBindingTest() CandidateTestBinaryBuildRecord {
	return CandidateTestBinaryBuildRecord{
		CandidateTree: strings.Repeat("a", 40), Package: "./internal/example", Mode: "test", Platform: "linux/amd64", GoToolchain: RequiredGoToolchain, CGOEnabled: true,
		ToolchainSHA256: "sha256:" + strings.Repeat("b", 64), BuildFlags: []string{"-trimpath"}, CompileClosureSHA256: "sha256:" + strings.Repeat("c", 64), ManifestSHA256: strings.Repeat("d", 64), ArtifactSHA256: "sha256:" + strings.Repeat("e", 64), BinarySize: 1,
		GoListWallMS: 1, BuildWallMS: 2, CompileActionMS: 3, LinkActionMS: 4, CompileCriticalWallMS: 5, GOCachePrivateHits: 6, GOCacheOCIProjectCacheHits: 7, GOCachePrivateRootIdentity: "sha256:" + strings.Repeat("f", 64), GOCacheMisses: 8, GOCachePuts: 9,
	}
}
