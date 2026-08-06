package sharedfilepath

import (
	"errors"
	"slices"
	"testing"
)

func TestWritePrefixesReturnsIndependentSnapshot(t *testing.T) {
	t.Parallel()

	first := WritePrefixes()
	first[0] = "mutated/"
	want := []string{"handoff/", "dag/", "inbox/", "reports/"}
	if got := WritePrefixes(); !slices.Equal(got, want) {
		t.Fatalf("WritePrefixes() = %#v, want %#v", got, want)
	}
}

func TestValidateWritePath_AcceptsPublicWritePrefixes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		path string
		want string
	}{
		{"handoff non-tasks", "handoff/task-1/notes.md", "handoff/task-1/notes.md"},
		{"handoff nested path", "handoff/runs/task-1.md", "handoff/runs/task-1.md"},
		{"dag node output", "dag/dag-1/node-a/output.json", "dag/dag-1/node-a/output.json"},
		{"inbox", "inbox/task-1/user-1.md", "inbox/task-1/user-1.md"},
		{"reports", "reports/task-1/result.md", "reports/task-1/result.md"},
		{"normalises backslash", "handoff\\task-1\\notes.md", "handoff/task-1/notes.md"},
		{"strips redundant ./", "./handoff/task-1/notes.md", "handoff/task-1/notes.md"},
		{"resolves intra-segment ..", "handoff/foo/../task-1/notes.md", "handoff/task-1/notes.md"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateWritePath(tt.path)
			if err != nil {
				t.Fatalf("ValidateWritePath(%q) error = %v, want ok", tt.path, err)
			}
			if got != tt.want {
				t.Fatalf("ValidateWritePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestValidateWritePath_RejectsProtectedInternalRoot(t *testing.T) {
	t.Parallel()

	if got, err := ValidateWritePath("_internal/runtime/state/thr-1.json"); err == nil {
		t.Fatalf("ValidateWritePath(_internal) = %q nil error, want protected-root rejection", got)
	}
}

func TestValidateWritePath_RejectsTraversalAndAbsoluteAndUnknown(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		wantSent error
	}{
		{"empty string", "", ErrPathEmpty},
		{"only whitespace", "   ", ErrPathEmpty},
		{"slash root", "/", ErrPathAbsolute},
		{"absolute unix", "/etc/passwd", ErrPathAbsolute},
		{"traversal at root", "../etc/passwd", ErrPathTraversal},
		{"traversal mid-path", "handoff/../../../etc", ErrPathTraversal},
		{"single dotdot", "..", ErrPathTraversal},
		{"unknown prefix", "secrets/passwd", ErrPathPrefixNotAllowed},
		{"prefix without slash boundary", "handoffx/task-1.md", ErrPathPrefixNotAllowed},
		{"top-level file with no whitelist prefix", "README.md", ErrPathPrefixNotAllowed},
		{"bare prefix without child", "handoff", ErrPathPrefixNotAllowed},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ValidateWritePath(tt.path)
			if err == nil {
				t.Fatalf("ValidateWritePath(%q) = %q, want err %v", tt.path, got, tt.wantSent)
			}
			if !errors.Is(err, tt.wantSent) {
				t.Fatalf("ValidateWritePath(%q) err = %v, want errors.Is(%v)", tt.path, err, tt.wantSent)
			}
		})
	}
}

func TestValidateAgentWritePath_AllowsWhitelistedHandoff(t *testing.T) {
	t.Parallel()

	got, err := ValidateAgentWritePath("handoff/agent-notes.md")
	if err != nil {
		t.Fatalf("ValidateAgentWritePath(agent handoff) error = %v, want ok", err)
	}
	if got != "handoff/agent-notes.md" {
		t.Fatalf("got = %q, want handoff/agent-notes.md", got)
	}
}

func TestValidateReadPath_SkipsPrefixWhitelistButKeepsTraversalChecks(t *testing.T) {
	t.Parallel()

	// Read path must allow legacy prefixes (backwards-compat for rows
	// created before the schema landed) yet still reject traversal /
	// absolute paths.
	cleaned, err := ValidateReadPath("legacy/old-row.md")
	if err != nil {
		t.Fatalf("ValidateReadPath(legacy) error = %v, want ok (no whitelist on read)", err)
	}
	if cleaned != "legacy/old-row.md" {
		t.Fatalf("cleaned = %q, want legacy/old-row.md", cleaned)
	}

	if _, err := ValidateReadPath("../etc/passwd"); !errors.Is(err, ErrPathTraversal) {
		t.Fatalf("traversal on read err = %v, want ErrPathTraversal", err)
	}
	if _, err := ValidateReadPath("/etc/passwd"); !errors.Is(err, ErrPathAbsolute) {
		t.Fatalf("absolute on read err = %v, want ErrPathAbsolute", err)
	}
}
