package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestCombinedMigrationUpgradeMatrixReaches123(t *testing.T) {
	for _, startVersion := range []int{0, 119, 120, 121, 122} {
		t.Run("start="+migrationVersionLabel(startVersion), func(t *testing.T) {
			t.Parallel()
			db := openMigrationTestDB(t)
			if startVersion != 0 {
				dir := copyMigrationsThroughVersion(t, "migrations", startVersion)
				if err := RunMigrations(context.Background(), db, dir); err != nil {
					t.Fatalf("RunMigrations(to %d) error = %v", startVersion, err)
				}
				assertMaxMigrationVersion(t, db, startVersion)
			}
			if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
				t.Fatalf("RunMigrations(%d→123) error = %v", startVersion, err)
			}
			if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
				t.Fatalf("RunMigrations(%d→123 repeat) error = %v", startVersion, err)
			}
			assertMaxMigrationVersion(t, db, 123)
			assertMigrationMarkerCount(t, db, terminalOutcomeOutboxMigration, 1)
			assertMigrationMarkerCount(t, db, canonicalManagedGeneration122, 1)
			assertMigrationMarkerCount(t, db, canonicalProviderRecovery123, 1)
		})
	}
}

func migrationVersionLabel(version int) string {
	if version == 0 {
		return "fresh"
	}
	return fmt.Sprint(version)
}

func TestLegacy120CompatibilityUpgradesOldAndCanonicalTargetStates(t *testing.T) {
	for _, kind := range []legacy120FixtureKind{managedGenerationFixture, providerRecoveryFixture} {
		for _, targetExists := range []bool{false, true} {
			t.Run(string(kind)+"/target="+boolLabel(targetExists), func(t *testing.T) {
				t.Parallel()
				fixture := setupLegacy120CompatibilityFixture(t, kind, targetExists)
				if err := RunMigrations(fixture.ctx, fixture.db, "migrations"); err != nil {
					t.Fatalf("RunMigrations() error = %v", err)
				}
				if err := RunMigrations(fixture.ctx, fixture.db, "migrations"); err != nil {
					t.Fatalf("RunMigrations(repeat) error = %v", err)
				}
				assertMaxMigrationVersion(t, fixture.db, 123)
				assertLegacy120CompatibilityApplied(t, fixture, targetExists)
			})
		}
	}
}

func boolLabel(value bool) string {
	if value {
		return "present"
	}
	return "absent"
}

func TestLegacy120CompatibilityRollsBackTerminalBodyFailure(t *testing.T) {
	for _, kind := range []legacy120FixtureKind{managedGenerationFixture, providerRecoveryFixture} {
		for _, targetExists := range []bool{false, true} {
			t.Run(string(kind)+"/target="+boolLabel(targetExists), func(t *testing.T) {
				t.Parallel()
				fixture := setupLegacy120CompatibilityFixture(t, kind, targetExists)
				before := legacy120DatabaseSnapshot(t, fixture.db)
				dir := legacy120CompatibilityDir(t, `
					CREATE TABLE legacy_compat_body_probe(id INTEGER PRIMARY KEY);
					INSERT INTO missing_legacy_compat_table(id) VALUES (1);
				`, fixture.targetFilename)

				err := RunMigrations(fixture.ctx, fixture.db, dir)
				if err == nil || !strings.Contains(err.Error(), "missing_legacy_compat_table") {
					t.Fatalf("RunMigrations() error = %v, want injected body failure", err)
				}
				assertLegacy120RollbackSnapshot(t, fixture.db, before)
			})
		}
	}
}

func TestLegacy120CompatibilityRollsBackTerminalMarkerFailure(t *testing.T) {
	for _, kind := range []legacy120FixtureKind{managedGenerationFixture, providerRecoveryFixture} {
		for _, targetExists := range []bool{false, true} {
			t.Run(string(kind)+"/target="+boolLabel(targetExists), func(t *testing.T) {
				t.Parallel()
				fixture := setupLegacy120CompatibilityFixture(t, kind, targetExists)
				installTerminalMarkerFailureTrigger(t, fixture.db)
				before := legacy120DatabaseSnapshot(t, fixture.db)
				dir := legacy120CompatibilityDir(
					t,
					readMigrationTestFile(t, terminalOutcomeOutboxMigration),
					fixture.targetFilename,
				)

				err := RunMigrations(fixture.ctx, fixture.db, dir)
				if err == nil || !strings.Contains(err.Error(), "injected terminal marker failure") {
					t.Fatalf("RunMigrations() error = %v, want injected marker failure", err)
				}
				assertLegacy120RollbackSnapshot(t, fixture.db, before)
			})
		}
	}
}

func installTerminalMarkerFailureTrigger(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		CREATE TRIGGER fail_terminal_outcome_marker
		BEFORE INSERT ON schema_migrations
		WHEN NEW.filename = '120_terminal_outcome_outbox.sql'
		BEGIN
			SELECT RAISE(ABORT, 'injected terminal marker failure');
		END
	`)
}

type legacy120RejectionCase struct {
	name         string
	kind         legacy120FixtureKind
	targetExists bool
	mutate       func(*testing.T, *sql.DB)
	wantError    string
}

var legacy120RejectionCases = []legacy120RejectionCase{
	{
		name: "forged marker identity",
		kind: managedGenerationFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "UPDATE schema_migrations SET name = 'forged' WHERE version = 120")
		},
		wantError: "forged name",
	},
	{
		name: "canonical target collision",
		kind: managedGenerationFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			insertMigrationMarker(t, db, 122, "122_conflicting_owner.sql", 7)
		},
		wantError: "marker collision",
	},
	{
		name:         "forged canonical target identity",
		kind:         managedGenerationFixture,
		targetExists: true,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `
				UPDATE schema_migrations SET name = 'forged-target'
				WHERE filename = '122_mcp_managed_generations.sql'
			`)
		},
		wantError: "marker identity is invalid",
	},
	{
		name: "canonical filename at wrong version",
		kind: managedGenerationFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			insertMigrationMarker(t, db, 918, canonicalManagedGeneration122, 7)
		},
		wantError: "marker collision",
	},
	{
		name: "partial managed schema",
		kind: managedGenerationFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "DROP TABLE mcp_managed_generations")
		},
		wantError: "table mcp_managed_generations is missing",
	},
	{
		name: "invalid managed owner",
		kind: managedGenerationFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, `
				PRAGMA ignore_check_constraints = ON;
				UPDATE mcp_managed_generation_owner SET singleton_id = 2;
				PRAGMA ignore_check_constraints = OFF
			`)
		},
		wantError: "owner identity is invalid",
	},
	{
		name: "missing provider trigger",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "DROP TRIGGER trg_prevent_agent_provider_binding_rebind")
		},
		wantError: "identity trigger is missing",
	},
	{
		name:         "recorded provider target with partial state",
		kind:         providerRecoveryFixture,
		targetExists: true,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "DROP TRIGGER trg_prevent_agent_provider_binding_rebind")
		},
		wantError: "identity trigger is missing",
	},
	{
		name: "invalid provider data",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mutateProviderRecoveryRow(t, db, "provider_thread_id = '019E218FB5147733BE85B3EE7F6A78B1'")
		},
		wantError: "provider_thread_id is not canonical",
	},
	{
		name: "invalid provider owner",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "UPDATE agent_provider_binding SET codex_home = '/wrong-owner'")
		},
		wantError: "does not match codex_home",
	},
	{
		name: "unknown provider",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mutateProviderRecoveryRow(t, db, "provider = 'unknown'")
		},
		wantError: "unsupported provider",
	},
	{
		name: "detached legacy marker",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			mustExec(t, db, "UPDATE schema_migrations SET version = 918 WHERE version = 120")
		},
		wantError: "without owning schema version 120",
	},
	{
		name: "third legacy marker",
		kind: providerRecoveryFixture,
		mutate: func(t *testing.T, db *sql.DB) {
			insertMigrationMarker(t, db, 919, legacyManagedGeneration120, 8)
		},
		wantError: "marker set is ambiguous",
	},
}

func TestLegacy120CompatibilityRejectsInvalidStatesWithoutSideEffects(t *testing.T) {
	for _, test := range legacy120RejectionCases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := setupLegacy120CompatibilityFixture(t, test.kind, test.targetExists)
			test.mutate(t, fixture.db)
			before := legacy120DatabaseSnapshot(t, fixture.db)
			dir := legacy120CompatibilityDir(
				t,
				readMigrationTestFile(t, terminalOutcomeOutboxMigration),
				fixture.targetFilename,
			)

			err := RunMigrations(fixture.ctx, fixture.db, dir)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("RunMigrations() error = %v, want %q", err, test.wantError)
			}
			assertLegacy120RollbackSnapshot(t, fixture.db, before)
		})
	}
}

func mutateProviderRecoveryRow(t *testing.T, db *sql.DB, assignment string) {
	t.Helper()
	mustExec(t, db, "DROP TRIGGER trg_prevent_agent_provider_binding_rebind")
	mustExec(t, db, "UPDATE agent_provider_binding SET "+assignment+" WHERE agent_id = 'compat-agent'")
	mustExec(t, db, agentProviderBindingIdentityTriggerSQL)
}
