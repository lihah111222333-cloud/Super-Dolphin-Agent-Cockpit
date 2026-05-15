package fbsd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTracker_DisabledNoOp(t *testing.T) {
	dir := t.TempDir()
	tr, err := NewTracker(filepath.Join(dir, "ws.json"), filepath.Join(dir, "gl.json"), false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Enabled() {
		t.Error("disabled tracker should report Enabled()=false")
	}
	tr.Record("x", "anchor")
	if err := tr.Flush(context.Background()); err != nil {
		t.Errorf("disabled Flush should be no-op: %v", err)
	}
}

func TestTracker_NilReceiverSafe(t *testing.T) {
	var tr *Tracker
	if tr.Enabled() {
		t.Error("nil receiver Enabled() must be false")
	}
	tr.Record("x", "")
	if err := tr.Flush(context.Background()); err != nil {
		t.Errorf("nil Flush must be no-op, got %v", err)
	}
	ws, gl := tr.Snapshot()
	if len(ws) != 0 || len(gl) != 0 {
		t.Errorf("nil Snapshot should return empty, got ws=%v gl=%v", ws, gl)
	}
}

func TestTracker_RecordAndFlushPersists(t *testing.T) {
	dir := t.TempDir()
	wsPath := filepath.Join(dir, "ws.json")
	glPath := filepath.Join(dir, "gl.json")
	// 用极长 saveInterval 避免节流 ticker 干扰：仅靠 stop drain 写盘
	tr, err := newTrackerWithInterval(wsPath, glPath, true, time.Hour)
	require.NoError(t, err)
	require.NoError(t, tr.Start())
	tr.Record("tdd", "red-green")
	tr.Record("tdd", "")
	tr.Record("brainstorming", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, tr.Flush(ctx))

	ws, err := LoadStats(wsPath)
	require.NoError(t, err)
	require.NotNil(t, ws["tdd"])
	require.Lenf(t, ws["tdd"].Calls, 2, "tdd Calls len=%d want 2: %+v", lenOrZero(ws["tdd"]), ws["tdd"])
	require.Equal(t, 1, ws["tdd"].SectionCalls["red-green"])
	require.NotNil(t, ws["brainstorming"])
	require.Len(t, ws["brainstorming"].Calls, 1)

	gl, err := LoadStats(glPath)
	require.NoError(t, err)
	require.Lenf(t, gl, 2, "global stats should have 2 skills, got %d", len(gl))
}

func TestTracker_SnapshotIsDeepCopy(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTrackerWithInterval(filepath.Join(dir, "ws.json"), filepath.Join(dir, "gl.json"), true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	tr.Record("x", "")
	// 给 worker 一点时间 apply
	for i := 0; i < 50; i++ {
		ws, _ := tr.Snapshot()
		if len(ws) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ws, _ := tr.Snapshot()
	if ws["x"] == nil {
		t.Fatal("Record/Snapshot timed out")
	}
	// mutate snapshot；不应影响 tracker 内部
	ws["x"].Calls = append(ws["x"].Calls, time.Now())
	ws["x"].SectionCalls["new"] = 99

	// 再 Snapshot 应该还是 1 call
	ws2, _ := tr.Snapshot()
	if len(ws2["x"].Calls) != 1 {
		t.Errorf("tracker mutated by snapshot caller: Calls=%v", ws2["x"].Calls)
	}
	if ws2["x"].SectionCalls["new"] != 0 {
		t.Errorf("tracker SectionCalls leaked: %v", ws2["x"].SectionCalls)
	}

	_ = tr.Flush(context.Background())
}

func TestTracker_RecordEmptyNameIgnored(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTrackerWithInterval(filepath.Join(dir, "ws.json"), filepath.Join(dir, "gl.json"), true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	tr.Record("", "anchor")
	_ = tr.Flush(context.Background())
	ws, _ := tr.Snapshot()
	if len(ws) != 0 {
		t.Errorf("empty name should be ignored, got %v", ws)
	}
}

func TestTracker_DoubleFlushSafe(t *testing.T) {
	dir := t.TempDir()
	tr, err := newTrackerWithInterval(filepath.Join(dir, "ws.json"), filepath.Join(dir, "gl.json"), true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Start(); err != nil {
		t.Fatal(err)
	}
	tr.Record("x", "")
	if err := tr.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tr.Flush(context.Background()); err != nil {
		t.Errorf("second Flush must be safe, got %v", err)
	}
}

func lenOrZero(s *SkillStats) int {
	if s == nil {
		return 0
	}
	return len(s.Calls)
}
