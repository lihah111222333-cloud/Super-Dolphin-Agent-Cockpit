package codexapp

import (
	"os"
	"path/filepath"
	"testing"
)

// writeRolloutFixture creates a single rollout jsonl under the codex
// home so the glob-based findRolloutPath has something to match.
func writeRolloutFixture(t *testing.T, root, threadID string) string {
	t.Helper()
	dir := filepath.Join(root, "sessions", "2026", "04", "23")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "rollout-abc-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestFindRolloutPathHonoursCodexHome verifies the P21 Track B plumb:
// a non-empty codexHome points the lookup at an alternate directory
// tree, so a rollout file written by a non-default codex instance is
// discoverable.
func TestFindRolloutPathHonoursCodexHome(t *testing.T) {
	t.Parallel()

	codexHome := t.TempDir()
	want := writeRolloutFixture(t, codexHome, "thread-multi")

	got, err := findRolloutPath("thread-multi", codexHome)
	if err != nil {
		t.Fatalf("findRolloutPath err = %v", err)
	}
	if got != want {
		t.Fatalf("findRolloutPath = %q, want %q", got, want)
	}
}

// TestFindRolloutPathFallsBackToLegacyHome guards the single-provider
// path: an empty codexHome must keep the pre-P21 ~/.codex lookup so
// deployments that never opt into multi-provider binding still find
// their rollout files.
func TestFindRolloutPathFallsBackToLegacyHome(t *testing.T) {
	// Cannot run parallel — t.Setenv serialises HOME mutation.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME", "1")

	want := writeRolloutFixture(t, filepath.Join(fakeHome, ".codex"), "thread-legacy")

	got, err := findRolloutPath("thread-legacy", "")
	if err != nil {
		t.Fatalf("findRolloutPath err = %v", err)
	}
	if got != want {
		t.Fatalf("findRolloutPath = %q, want %q", got, want)
	}
}

// TestFindRolloutPathNotFound asserts the error path unchanged: when
// no matching rollout exists, the caller sees a descriptive error so
// history fallback can log + continue.
func TestFindRolloutPathRequiresExplicitLegacyOptIn(t *testing.T) {
	t.Setenv("CODEXAPP_ALLOW_LEGACY_DEFAULT_HOME", "")
	if _, err := findRolloutPath("thread-legacy", ""); err == nil {
		t.Fatal("expected codex home required error without legacy opt-in")
	}
}

func TestFindRolloutPathNotFound(t *testing.T) {
	t.Parallel()

	codexHome := t.TempDir()
	if _, err := findRolloutPath("missing-thread", codexHome); err == nil {
		t.Fatal("expected error for missing rollout")
	}
}

// TestResolveRolloutRootTrimsWhitespace checks that a codexHome with
// incidental whitespace still wins over the legacy fallback. The
// session's runtimeConfigString trims on read, but we also trim here
// so a hand-constructed test input can't silently fall through.
func TestResolveRolloutRootTrimsWhitespace(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got, err := resolveRolloutRoot("  " + dir + "  ")
	if err != nil {
		t.Fatalf("resolveRolloutRoot err = %v", err)
	}
	if got != dir {
		t.Fatalf("resolveRolloutRoot = %q, want %q", got, dir)
	}
}
