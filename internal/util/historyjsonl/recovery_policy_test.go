package historyjsonl

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestMissingClassificationDoesNotTreatArbitraryENOENTAsRootMissing(t *testing.T) {
	t.Parallel()

	childErr := &fs.PathError{Op: "open", Path: "projects/disappeared/session.jsonl", Err: os.ErrNotExist}
	if IsMissingProviderHistory(childErr) {
		t.Fatalf("IsMissingProviderHistory(%v) = true, want false for child ENOENT", childErr)
	}
}

func TestDiscoverClaudePathPropagatesWalkChildDisappear(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	if err := os.Mkdir(projects, 0o700); err != nil {
		t.Fatalf("mkdir projects: %v", err)
	}
	childErr := &fs.PathError{
		Op:   "lstat",
		Path: filepath.Join(projects, "disappeared"),
		Err:  os.ErrNotExist,
	}
	ops := discoveryOps{
		stat: os.Stat,
		walkDir: func(_ string, _ fs.WalkDirFunc) error {
			return childErr
		},
	}

	_, err := discoverClaudePathWithOps(ReadRequest{
		Provider:         "claude",
		ProviderThreadID: testRecoveryPolicyUUID,
		ClaudeHome:       root,
	}, ops)
	if !errors.Is(err, childErr) {
		t.Fatalf("discoverClaudePathWithOps() error = %v, want child disappearance %v", err, childErr)
	}
	if IsMissingProviderHistory(err) {
		t.Fatalf("walk child disappearance = %v, must not be root missing", err)
	}
}

const testRecoveryPolicyUUID = "019e218f-b514-7733-be85-b3ee7f6a78a6"
