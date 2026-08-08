package gate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLoadWorkloadCatalogRecordByObservationIdentity(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	now := time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC)
	catalog := testObservationIdentityCatalog(strings.Repeat("a", 64))
	observation := WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("b", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         now,
	}
	if err := store.RecordWorkloadCatalog(catalog, observation); err != nil {
		t.Fatal(err)
	}
	record, err := store.LoadWorkloadCatalogRecordByObservationIdentity(
		observation.SourceTreeSHA,
		observation.Entrypoint,
		observation.Profile,
		observation.AcceptedGeneration,
	)
	if err != nil {
		t.Fatalf("load catalog by observation identity: %v", err)
	}
	if !strings.HasPrefix(record.CatalogDigest, "sha256:") {
		t.Fatalf("catalog digest = %q, want prefixed SHA-256", record.CatalogDigest)
	}
	if len(record.Observations) != 1 || record.Observations[0] != observation {
		t.Fatalf("observations = %#v, want %#v", record.Observations, []WorkloadCatalogObservation{observation})
	}
	if got, want := record.Catalog.Workloads[0].CommandDigest, catalog.Workloads[0].CommandDigest; got != want {
		t.Fatalf("loaded command digest = %q, want %q", got, want)
	}
}

func TestLoadWorkloadCatalogRecordByObservationIdentityReturnsNotFound(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	_, err := store.LoadWorkloadCatalogRecordByObservationIdentity(
		strings.Repeat("c", 40),
		CIEntrypointGitPreCommit,
		ProfileLocalFast,
		1,
	)
	if !errors.Is(err, ErrWorkloadCatalogObservationNotFound) {
		t.Fatalf("not-found error = %v, want ErrWorkloadCatalogObservationNotFound", err)
	}
}

func TestLoadWorkloadCatalogRecordByObservationIdentityRejectsMultipleCatalogs(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	observation := WorkloadCatalogObservation{
		SourceTreeSHA:      strings.Repeat("d", 40),
		Entrypoint:         CIEntrypointGitPreCommit,
		Profile:            ProfileLocalFast,
		AcceptedGeneration: 1,
		ObservedAt:         time.Date(2026, time.August, 5, 1, 2, 3, 0, time.UTC),
	}
	for _, commandDigest := range []string{strings.Repeat("1", 64), strings.Repeat("2", 64)} {
		if err := store.RecordWorkloadCatalog(testObservationIdentityCatalog(commandDigest), observation); err != nil {
			t.Fatalf("record catalog %s: %v", commandDigest[:8], err)
		}
	}
	_, err := store.LoadWorkloadCatalogRecordByObservationIdentity(
		observation.SourceTreeSHA,
		observation.Entrypoint,
		observation.Profile,
		observation.AcceptedGeneration,
	)
	if err == nil || !strings.Contains(err.Error(), "multiple catalogs") {
		t.Fatalf("multiple-catalog error = %v, want fail-fast error", err)
	}
}

func TestDurationLedgerSQLiteCatalogObservationIdentityIndexAndQueryPlan(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	indexes := sqliteIndexListForTest(t, database, "ci_catalog_observations")
	index, ok := indexes["idx_ci_catalog_observations_identity_catalog"]
	if !ok {
		t.Fatal("catalog observation identity index was not initialized")
	}
	got := sqliteIndexColumnsForTest(t, database, index.name)
	want := []sqliteIndexColumnExpectation{
		{name: "source_tree_sha"}, {name: "entrypoint"}, {name: "profile"},
		{name: "accepted_generation"}, {name: "catalog_digest"},
	}
	if !equalSQLiteIndexColumns(got, want) {
		t.Fatalf("identity index columns = %#v, want %#v", got, want)
	}
	details := sqliteQueryPlanDetails(t, database, workloadCatalogObservationIdentityQuery,
		strings.Repeat("a", 40), string(CIEntrypointGitPreCommit), string(ProfileLocalFast), "1")
	assertSQLiteQueryPlanAccess(t, details, []string{"USING COVERING INDEX idx_ci_catalog_observations_identity_catalog"})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"ci_catalog_observations"})
}

func testObservationIdentityCatalog(commandDigest string) WorkloadCatalog {
	return WorkloadCatalog{
		Version:       durationLedgerVersion,
		Authoritative: true,
		Workloads: []Workload{{
			ID:                  string(GateIDAIMaintenanceSelfTest),
			Kind:                WorkloadKindGuard,
			CommandDigest:       commandDigest,
			BootstrapEstimateMS: 1_000,
			Shardable:           true,
		}},
	}
}

func equalSQLiteIndexColumns(got, want []sqliteIndexColumnExpectation) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
