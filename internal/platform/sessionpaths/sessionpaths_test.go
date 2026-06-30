package sessionpaths

import (
	"path/filepath"
	"testing"
)

func TestCodexRolloutGlob(t *testing.T) {
	t.Parallel()

	got, err := CodexRolloutGlob("  "+filepath.Join("home", "codex")+"  ", "  thread-123  ")
	if err != nil {
		t.Fatalf("CodexRolloutGlob() error = %v", err)
	}

	want := filepath.Join("home", "codex", "sessions", "*", "*", "*", "rollout-*-thread-123.jsonl")
	if got != want {
		t.Fatalf("CodexRolloutGlob() = %q, want %q", got, want)
	}
}

func TestCodexRolloutGlobRequiresInputs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		codexHome string
		threadID  string
	}{
		{name: "empty codex home", codexHome: "   ", threadID: "thread-123"},
		{name: "empty thread id", codexHome: filepath.Join("home", "codex"), threadID: "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := CodexRolloutGlob(tc.codexHome, tc.threadID); err == nil {
				t.Fatal("CodexRolloutGlob() error = nil, want non-nil")
			}
		})
	}
}

func TestManagedScratchpadDir(t *testing.T) {
	t.Parallel()

	tempRoot := filepath.Join("tmp", "root")
	got := ManagedScratchpadDir(tempRoot, "  /Users/Alice/My Project  ", "  thread-1  ")
	want := filepath.Join(tempRoot, "super-agent-v3", "users-alice-my-project", "thread-1", "scratchpad")
	if got != want {
		t.Fatalf("ManagedScratchpadDir() = %q, want %q", got, want)
	}
}

func TestIsManagedScratchpadDir(t *testing.T) {
	t.Parallel()

	tempRoot := t.TempDir()
	root := filepath.Join(tempRoot, "super-agent-v3")
	managed := filepath.Join(root, "project", "thread-1", "scratchpad")

	for _, tc := range []struct {
		name string
		dir  string
		want bool
	}{
		{name: "managed child", dir: managed, want: true},
		{name: "trimmed managed child", dir: "  " + managed + "  ", want: true},
		{name: "namespace root", dir: root, want: false},
		{name: "dot", dir: ".", want: false},
		{name: "filesystem root", dir: string(filepath.Separator), want: false},
		{name: "outside namespace", dir: filepath.Join(tempRoot, "other", "thread-1"), want: false},
		{name: "current dotdot prefix child behavior", dir: filepath.Join(root, "..hidden"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsManagedScratchpadDir(tempRoot, tc.dir); got != tc.want {
				t.Fatalf("IsManagedScratchpadDir() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSanitizeProjectPath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "ascii path", raw: "  /Users/Alice/My Project  ", want: "users-alice-my-project"},
		{name: "unicode letters", raw: "项目/Alpha-42", want: "项目-alpha-42"},
		{name: "punctuation hash", raw: "!!!", want: "project-e84c538e"},
		{name: "empty hash", raw: "  ", want: "project-e3b0c442"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := SanitizeProjectPath(tc.raw); got != tc.want {
				t.Fatalf("SanitizeProjectPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
