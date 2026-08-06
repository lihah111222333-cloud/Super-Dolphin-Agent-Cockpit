package multilsp

import (
	"errors"
	"testing"
)

func TestGoplsRootCohortControllerReusesOneCohortAndRejectsConflict(t *testing.T) {
	controller := NewGoplsRootCohortController()
	config := testGoplsRootCohortConfig("cohort-a", "config-a")
	first, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease(first) error = %v", err)
	}
	second, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease(second) error = %v", err)
	}
	if first.Config().CohortID != second.Config().CohortID {
		t.Fatalf("same root created different cohorts: first=%q second=%q", first.Config().CohortID, second.Config().CohortID)
	}
	if first.Fence().LeaseID == second.Fence().LeaseID {
		t.Fatal("same cohort leases reused a lease ID")
	}
	snapshot, ok := controller.Snapshot(config)
	if !ok || snapshot.ActiveMembers != 2 || snapshot.State != GoplsRootCohortStateAdmitted {
		t.Fatalf("snapshot = %#v, ok=%v; want one admitted cohort with two members", snapshot, ok)
	}
	conflict := config
	conflict.EffectiveConfigDigest = "config-b"
	if _, err := controller.AcquireLease(conflict); !errors.Is(err, ErrGoplsRootCohortConfigConflict) {
		t.Fatalf("AcquireLease(conflict) error = %v, want immutable config conflict", err)
	}
	proofConflict := config
	proofConflict.RepositoryInstanceProof.GitMarkerDigest = "marker-b"
	if _, err := controller.AcquireLease(proofConflict); !errors.Is(err, ErrGoplsRootCohortConfigConflict) {
		t.Fatalf("AcquireLease(proof conflict) error = %v, want immutable config conflict", err)
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release(first) error = %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("Release(second) error = %v", err)
	}
	snapshot, ok = controller.Snapshot(config)
	if !ok || snapshot.ActiveMembers != 0 || snapshot.State != GoplsRootCohortStateIdle {
		t.Fatalf("idle snapshot = %#v, ok=%v; want zero active members", snapshot, ok)
	}
	third, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease(third) error = %v", err)
	}
	if third.Config().CohortID != config.CohortID {
		t.Fatalf("re-admission changed cohort ID: got=%q want=%q", third.Config().CohortID, config.CohortID)
	}
	if err := third.Release(); err != nil {
		t.Fatalf("Release(third) error = %v", err)
	}
}

func TestGoplsRootCohortFenceRejectsReleaseAndConfigReplay(t *testing.T) {
	controller := NewGoplsRootCohortController()
	config := testGoplsRootCohortConfig("cohort-fence", "config-fence")
	lease, err := controller.AcquireLease(config)
	if err != nil {
		t.Fatalf("AcquireLease() error = %v", err)
	}
	fence := lease.Fence()
	if err := controller.ValidateFence(config, fence); err != nil {
		t.Fatalf("ValidateFence(current) error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := controller.ValidateFence(config, fence); !errors.Is(err, ErrGoplsRootCohortFenceStale) {
		t.Fatalf("ValidateFence(released) error = %v, want stale fence", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second Release() error = %v, want idempotent release", err)
	}
	conflict := config
	conflict.CohortID = "cohort-other"
	if err := controller.ValidateFence(conflict, fence); err == nil {
		t.Fatal("ValidateFence(conflicting config) unexpectedly succeeded")
	}
	if err := controller.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := controller.AcquireLease(config); !errors.Is(err, ErrGoplsRootCohortClosed) {
		t.Fatalf("AcquireLease(after Close) error = %v, want closed", err)
	}
}

func TestGoplsRootCohortConfigRequiresTypedProof(t *testing.T) {
	config := GoplsRootCohortConfig{CohortID: "cohort", EffectiveConfigDigest: "digest"}
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() unexpectedly accepted missing repository proof")
	}
}

func testGoplsRootCohortConfig(cohortID, configDigest string) GoplsRootCohortConfig {
	return GoplsRootCohortConfig{
		CohortID: cohortID,
		RepositoryInstanceProof: GoplsRepositoryInstanceProof{
			CanonicalRootDigest: "root-digest",
			FilesystemIdentity:  "dev:inode",
			GitMarkerDigest:     "git-marker",
			InstanceNonce:       "instance-nonce",
		},
		EffectiveConfigDigest: configDigest,
	}
}
