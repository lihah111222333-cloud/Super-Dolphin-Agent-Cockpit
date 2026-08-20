package heterociowner

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type restartPlanFixture struct {
	fence     heteroci.OwnerFence
	planned   DurableReservedRoutingPlan
	manifest  heteroci.Manifest
	snapshot  heteroci.SnapshotSet
	units     []heteroci.ExecutionUnit
	operation heteroci.OperationID
	now       time.Time
}

func TestOwnerRestartReloadsFenceAndReplaysPlanBeforeStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heteroci-owner.db")
	fixture := createPlanBeforeRestart(t, path)
	reopened := openRestartDatabase(t, path)
	restarted, err := NewOwner(context.Background(), reopened, validOwnerScope())
	if err != nil {
		t.Fatalf("restart NewOwner: %v", err)
	}
	assertRestartPlanReplay(t, restarted, fixture)
	assertRestartStart(t, restarted, fixture)
}

func createPlanBeforeRestart(t *testing.T, path string) restartPlanFixture {
	t.Helper()
	db := openRestartDatabase(t, path)
	owner, err := NewOwner(context.Background(), db, validOwnerScope())
	if err != nil {
		t.Fatalf("NewOwner: %v", err)
	}
	now := time.UnixMilli(durableRoutingNowUnixMilli)
	owner.now = func() time.Time { return now }
	fence, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	manifest := durableRoutingManifest()
	snapshot := durableRoutingSnapshot(manifest, fence.OwnerEpoch, now)
	units := []heteroci.ExecutionUnit{{UnitID: "unit-a", TargetPlatform: heteroci.PlatformLinuxAMD64, RequiredRuntime: heteroci.RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 512 * 1024 * 1024}}
	operationID := heteroci.OperationID(strings.Repeat("f", 64))
	planned, err := owner.PlanAndReserve(context.Background(), fence, operationID, "run-restart", manifest, snapshot, units, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanAndReserve: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}
	return restartPlanFixture{fence: fence, planned: planned, manifest: manifest, snapshot: snapshot, units: units, operation: operationID, now: now}
}

func assertRestartPlanReplay(t *testing.T, restarted *Owner, fixture restartPlanFixture) {
	t.Helper()
	restarted.now = func() time.Time { return fixture.now }
	current, err := restarted.CurrentFence(context.Background())
	if err != nil || current != fixture.planned.Fence {
		t.Fatalf("restart CurrentFence = %#v, %v; want %#v", current, err, fixture.planned.Fence)
	}
	replay, err := restarted.PlanAndReserve(context.Background(), fixture.fence, fixture.operation, "run-restart", fixture.manifest, fixture.snapshot, fixture.units, 30*time.Second)
	if err != nil {
		t.Fatalf("restart replay: %v", err)
	}
	if !reflect.DeepEqual(replay, fixture.planned) {
		t.Fatalf("restart replay drifted:\n got %#v\nwant %#v", replay, fixture.planned)
	}
}

func assertRestartStart(t *testing.T, restarted *Owner, fixture restartPlanFixture) {
	t.Helper()
	startAt := fixture.now.Add(time.Second)
	restarted.now = func() time.Time { return startAt }
	currentSnapshot := durableRoutingSnapshot(fixture.manifest, fixture.fence.OwnerEpoch, startAt)
	started, err := restarted.Start(context.Background(), fixture.planned.Fence, heteroci.OperationID(strings.Repeat("e", 64)), "run-restart", "unit-a", currentSnapshot)
	if err != nil {
		t.Fatalf("restart Start: %v", err)
	}
	if started.Occupancy.OwnerEpoch != fixture.fence.OwnerEpoch || started.Occupancy.RunID != "run-restart" {
		t.Fatalf("restart occupancy = %#v", started.Occupancy)
	}
}

func openRestartDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, path)
	if err != nil {
		t.Fatalf("open restart database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
