package difftracker

import (
	"strings"
	"testing"
)

func TestMergeIntoSession(t *testing.T) {
	session := &agentDiffSession{files: make(map[string]*FileDiff)}
	changed := mergeIntoSession(session,
		buildUnifiedDiffBlock("b.txt", "", "beta\n")+
			buildUnifiedDiffBlock("a.txt", "old\n", "new\n"),
		nil,
	)

	if !changed {
		t.Fatal("mergeIntoSession() = false, want true")
	}
	if len(session.files) != 2 {
		t.Fatalf("len(files) = %d, want 2", len(session.files))
	}
}

func TestBuildCumulativeDiff(t *testing.T) {
	session := &agentDiffSession{
		files: map[string]*FileDiff{
			"b.txt": {Path: "b.txt", Diff: buildUnifiedDiffBlock("b.txt", "", "beta\n")},
			"a.txt": {Path: "a.txt", Diff: buildUnifiedDiffBlock("a.txt", "old\n", "new\n")},
		},
	}
	diffText := buildCumulativeDiff(session)
	firstA := strings.Index(diffText, "--- a/a.txt")
	firstB := strings.Index(diffText, "+++ b/b.txt")

	if firstA == -1 || firstB == -1 {
		t.Fatalf("unexpected diff text: %q", diffText)
	}
	if firstA > firstB {
		t.Fatalf("diff blocks are not sorted: %q", diffText)
	}
}

func TestMergeIntoSession_SameFile(t *testing.T) {
	session := &agentDiffSession{files: make(map[string]*FileDiff)}
	mergeIntoSession(session, buildUnifiedDiffBlock("a.txt", "old\n", "new\n"), nil)
	mergeIntoSession(session, buildUnifiedDiffBlock("a.txt", "new\n", "newer\n"), nil)

	diffText := buildCumulativeDiff(session)
	if strings.Contains(diffText, "-old") {
		t.Fatalf("old diff block should be replaced: %q", diffText)
	}
	if !strings.Contains(diffText, "-new") || !strings.Contains(diffText, "+newer") {
		t.Fatalf("replacement diff missing: %q", diffText)
	}
}

func TestMergeIntoSession_EmptyDiffClearsTouchedFiles(t *testing.T) {
	session := &agentDiffSession{files: make(map[string]*FileDiff)}
	mergeIntoSession(session, buildUnifiedDiffBlock("a.txt", "old\n", "new\n"), nil)

	changed := mergeIntoSession(session, "", []string{"a.txt"})
	if !changed {
		t.Fatal("mergeIntoSession() = false, want true when clearing touched file")
	}
	if diffText := buildCumulativeDiff(session); diffText != "" {
		t.Fatalf("buildCumulativeDiff() = %q, want empty after clear", diffText)
	}
}
