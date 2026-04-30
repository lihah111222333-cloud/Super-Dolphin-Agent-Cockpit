package fbsd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadStats_MissingFileReturnsEmpty(t *testing.T) {
	got, err := LoadStats("/nonexistent/skills-stats.json")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty Stats, got %v", got)
	}
}

func TestLoadStats_MalformedReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStats(path); err == nil {
		t.Fatal("malformed JSON should return error")
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	in := Stats{
		"x": &SkillStats{
			Calls:        []time.Time{t1},
			InstalledAt:  t1.Add(-7 * 24 * time.Hour),
			SectionCalls: map[string]int{"foo": 3},
		},
	}
	if err := SaveStats(path, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadStats(path)
	if err != nil {
		t.Fatal(err)
	}
	xOut := out["x"]
	if xOut == nil {
		t.Fatal("x missing after roundtrip")
	}
	if len(xOut.Calls) != 1 || !xOut.Calls[0].Equal(t1) {
		t.Errorf("Calls mismatch: %v", xOut.Calls)
	}
	if !xOut.InstalledAt.Equal(in["x"].InstalledAt) {
		t.Errorf("InstalledAt mismatch")
	}
	if xOut.SectionCalls["foo"] != 3 {
		t.Errorf("SectionCalls mismatch: %v", xOut.SectionCalls)
	}
}

func TestSaveStats_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "stats.json")
	if err := SaveStats(path, Stats{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestSaveStats_NoStaleTmpAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	if err := SaveStats(path, Stats{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("stale .tmp should be cleaned (rename moved it)")
	}
}

func TestSaveStats_NilStatsWritesEmptyObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stats.json")
	if err := SaveStats(path, nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// JSON 渲染应为 {} 不是 null
	if string(body) != "{}" {
		t.Errorf("nil stats should render as {}, got: %s", body)
	}
}

func TestSaveStats_EmptyPathErrors(t *testing.T) {
	if err := SaveStats("", Stats{}); err == nil {
		t.Error("empty path should error")
	}
}
