package gate

import (
	"strings"
	"testing"
)

func TestLoadAuthoritativeRemoteCIRunWorkloadCoverageRequiresExactFullAuthority(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, identity, receipts := recordWorkloadPassRun(t, store, "calibration-coverage", 1, "calibration-coverage")
	assertRemoteCICalibrationCoverageMissing(t, store, record, record.ImageCacheSnapshotID, "provisional")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	assertRemoteCICalibrationCoverageFound(t, store, record, identity)
	assertRemoteCICalibrationCoverageMissing(t, store, record, "other-snapshot", "snapshot drift")
}

func assertRemoteCICalibrationCoverageFound(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, want WorkloadPassIdentity) {
	t.Helper()
	coverage, found, err := store.LoadAuthoritativeRemoteCIRunWorkloadCoverage(record.Entrypoint, record.Profile, record.CatalogDigest, record.SourceTreeSHA, record.ImageCacheSnapshotID)
	if err != nil {
		t.Fatalf("LoadAuthoritativeRemoteCIRunWorkloadCoverage() error = %v", err)
	}
	if !found || len(coverage) != 1 || coverage[0] != want {
		t.Fatalf("authoritative coverage = %#v, found=%t, want %#v", coverage, found, want)
	}
}

func assertRemoteCICalibrationCoverageMissing(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, snapshotID, label string) {
	t.Helper()
	coverage, found, err := store.LoadAuthoritativeRemoteCIRunWorkloadCoverage(record.Entrypoint, record.Profile, record.CatalogDigest, record.SourceTreeSHA, snapshotID)
	if err != nil || found || len(coverage) != 0 {
		t.Fatalf("%s coverage = %#v, found=%t, err=%v", label, coverage, found, err)
	}
}

func TestLoadAuthoritativeRemoteCIRunWorkloadCoverageRejectsCorruptExecutedOrigin(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 1)
	record, _, receipts := recordWorkloadPassRun(t, store, "calibration-corrupt-origin", 1, "calibration-corrupt-origin")
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, true); err != nil {
		t.Fatal(err)
	}
	database := openWorkloadPassDatabase(t, store)
	if _, err := database.Exec(`UPDATE ci_run_workload_results SET origin_job_id = 'other-job' WHERE job_id = ?`, record.JobID); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	_, _, err := store.LoadAuthoritativeRemoteCIRunWorkloadCoverage(
		record.Entrypoint, record.Profile, record.CatalogDigest, record.SourceTreeSHA, record.ImageCacheSnapshotID,
	)
	if err == nil || !strings.Contains(err.Error(), "origin does not match run") {
		t.Fatalf("corrupt executed origin error = %v", err)
	}
}
