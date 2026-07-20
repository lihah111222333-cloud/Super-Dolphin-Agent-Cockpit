package sourceexport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadGitTreeReadsCommittedBlobsAndModes(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "README.md", "committed\n", 0o644)
	writeGitTreeFile(t, repo, "scripts/run.sh", "#!/bin/sh\n", 0o755)
	commitGitTreeFixture(t, repo)

	commit, entries, err := loadGitTree(context.Background(), execGitRunner{}, repo, "HEAD")
	if err != nil {
		t.Fatalf("loadGitTree() error = %v", err)
	}
	if len(commit) != 40 {
		t.Fatalf("commit length = %d, want 40", len(commit))
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Path != "README.md" || string(entries[0].Data) != "committed\n" || entries[0].Mode != "100644" {
		t.Fatalf("README entry = %#v", entries[0])
	}
	if entries[1].Path != "scripts/run.sh" || entries[1].Mode != "100755" {
		t.Fatalf("script entry = %#v", entries[1])
	}
}

func TestEnsureSourceCleanRejectsTrackedChanges(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "README.md", "committed\n", 0o644)
	commitGitTreeFixture(t, repo)
	writeGitTreeFile(t, repo, "README.md", "dirty\n", 0o644)

	err := ensureSourceClean(context.Background(), execGitRunner{}, repo)
	assertErrorCode(t, err, CodeSourceDirty)
}

func TestLoadGitTreeRejectsSymlink(t *testing.T) {
	repo := newGitTreeFixture(t)
	writeGitTreeFile(t, repo, "target.txt", "target\n", 0o644)
	if err := os.Symlink("target.txt", filepath.Join(repo, "link.txt")); err != nil {
		t.Fatal(err)
	}
	commitGitTreeFixture(t, repo)

	_, _, err := loadGitTree(context.Background(), execGitRunner{}, repo, "HEAD")
	assertErrorCode(t, err, CodeSymlinkRejected)
}

func TestValidateTreeEntriesRejectsCaseCollisionAndSubmodule(t *testing.T) {
	tests := []struct {
		name    string
		entries []TreeEntry
		code    Code
	}{
		{name: "case collision", entries: []TreeEntry{{Path: "README.md", Mode: "100644"}, {Path: "readme.md", Mode: "100644"}}, code: CodeCaseCollision},
		{name: "submodule", entries: []TreeEntry{{Path: "vendor/repo", Mode: "160000"}}, code: CodeForbiddenPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertErrorCode(t, validateTreeEntries(tt.entries), tt.code)
		})
	}
}

func TestLoadTreeEntryDataUsesOneBatchAndReusesDuplicateOID(t *testing.T) {
	const (
		firstOID  = "1111111111111111111111111111111111111111"
		secondOID = "2222222222222222222222222222222222222222"
	)
	entries := make([]TreeEntry, 5_000)
	for index := range entries {
		oid := firstOID
		if index%2 != 0 {
			oid = secondOID
		}
		entries[index] = TreeEntry{Path: "file", Mode: "100644", Hash: oid}
	}
	runner := &gitTreeRunnerStub{batchOutput: []byte(
		firstOID + " blob 3\none\n" +
			secondOID + " blob 3\ntwo\n",
	)}

	if err := loadTreeEntryData(context.Background(), runner, "/repo", entries); err != nil {
		t.Fatalf("loadTreeEntryData() error = %v", err)
	}
	if runner.batchCalls != 1 {
		t.Fatalf("batch calls = %d, want 1", runner.batchCalls)
	}
	if got, want := string(runner.batchInput), firstOID+"\n"+secondOID+"\n"; got != want {
		t.Fatalf("batch input = %q, want %q", got, want)
	}
	if string(entries[0].Data) != "one" || string(entries[1].Data) != "two" {
		t.Fatalf("entry data = %q, %q", entries[0].Data, entries[1].Data)
	}
	if &entries[0].Data[0] != &entries[2].Data[0] {
		t.Fatal("duplicate OID did not reuse validated blob data")
	}
}

func TestParseGitBlobBatchRejectsProtocolDrift(t *testing.T) {
	const oid = "1111111111111111111111111111111111111111"
	tests := []struct {
		name   string
		output string
	}{
		{name: "OID", output: strings.Repeat("2", 40) + " blob 3\none\n"},
		{name: "type", output: oid + " tree 3\none\n"},
		{name: "size", output: oid + " blob 4\none\n"},
		{name: "terminator", output: oid + " blob 3\none!"},
		{name: "trailing", output: oid + " blob 3\none\nextra"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := parseGitBlobBatch([]byte(test.output), []string{oid}); err == nil {
				t.Fatal("parseGitBlobBatch() accepted malformed batch output")
			}
		})
	}
}

func TestLoadTreeEntryDataHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	runner := &gitTreeRunnerStub{batch: func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	entries := []TreeEntry{{Path: "file", Mode: "100644", Hash: strings.Repeat("1", 40)}}
	started := time.Now()
	err := loadTreeEntryData(ctx, runner, "/repo", entries)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("loadTreeEntryData() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("context cancellation took %s", elapsed)
	}
}

func TestLoadTreeEntryDataRejectsEntryAndByteLimits(t *testing.T) {
	runner := &gitTreeRunnerStub{}
	entries := make([]TreeEntry, maxGitTreeEntries+1)
	if err := loadTreeEntryData(context.Background(), runner, "/repo", entries); err == nil {
		t.Fatal("loadTreeEntryData() accepted excessive entry count")
	}
	if runner.batchCalls != 0 {
		t.Fatalf("batch calls after entry limit = %d, want 0", runner.batchCalls)
	}
	oid := strings.Repeat("1", 40)
	output := []byte(oid + " blob " + fmt.Sprint(maxGitTreeBytes+1) + "\n")
	if _, err := parseGitBlobBatch(output, []string{oid}); err == nil {
		t.Fatal("parseGitBlobBatch() accepted excessive blob size")
	}
}

type gitTreeRunnerStub struct {
	batch       func(context.Context, []byte) ([]byte, error)
	batchOutput []byte
	batchInput  []byte
	batchCalls  int
}

func (runner *gitTreeRunnerStub) Run(context.Context, string, ...string) ([]byte, error) {
	return nil, errors.New("unexpected non-batch Git command")
}

func (runner *gitTreeRunnerStub) RunBatch(ctx context.Context, _ string, input []byte, _ int64, args ...string) ([]byte, error) {
	if strings.Join(args, " ") != "cat-file --batch" {
		return nil, errors.New("unexpected batch Git command")
	}
	runner.batchCalls++
	runner.batchInput = bytes.Clone(input)
	if runner.batch != nil {
		return runner.batch(ctx, input)
	}
	return bytes.Clone(runner.batchOutput), nil
}

func newGitTreeFixture(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	runGitTreeCommand(t, repo, "init", "-q")
	runGitTreeCommand(t, repo, "config", "user.email", "test@example.com")
	runGitTreeCommand(t, repo, "config", "user.name", "Source Export Test")
	return repo
}

func writeGitTreeFile(t *testing.T, repo string, name string, content string, mode os.FileMode) {
	t.Helper()
	filePath := filepath.Join(repo, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func commitGitTreeFixture(t *testing.T, repo string) {
	t.Helper()
	runGitTreeCommand(t, repo, "add", "--all")
	runGitTreeCommand(t, repo, "commit", "-qm", "fixture")
}

func runGitTreeCommand(t *testing.T, repo string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}
