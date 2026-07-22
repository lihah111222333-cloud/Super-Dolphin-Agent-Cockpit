package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCacheKeyIgnoresWorktreeAbsolutePath(t *testing.T) {
	first := newCacheFixture(t, "first")
	second := newCacheFixture(t, "second")
	firstKey, firstOutputs, err := requestKey(first)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, secondOutputs, err := requestKey(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey != secondKey {
		t.Fatalf("same relative inputs produced different keys:\nfirst=%s\nsecond=%s", firstKey, secondKey)
	}
	if !equalStrings(firstOutputs, secondOutputs) {
		t.Fatalf("outputs differ: %v vs %v", firstOutputs, secondOutputs)
	}
}

func TestSaveAndRestoreAcrossLinkedWorktrees(t *testing.T) {
	repository := initRepository(t)
	first := addWorktree(t, repository, "first")
	second := addWorktree(t, repository, "second")
	writeFixtureTree(t, first)
	writeFixtureTree(t, second)
	firstRequest := fixtureRequest(first)
	secondRequest := fixtureRequest(second)

	writeFile(t, filepath.Join(first, "bin", "tool"), "first artifact", 0o755)
	if err := save(firstRequest); err != nil {
		t.Fatal(err)
	}
	hit, err := restore(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("expected shared cache hit")
	}
	data, err := os.ReadFile(filepath.Join(second, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first artifact" {
		t.Fatalf("restored content = %q", data)
	}
	info, err := os.Stat(filepath.Join(second, "bin", "tool"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("restored mode = %o", info.Mode().Perm())
	}
}

func TestMakefileChangeMissesSharedCacheAcrossLinkedWorktrees(t *testing.T) {
	repository := initRepository(t)
	first := addWorktree(t, repository, "first")
	second := addWorktree(t, repository, "second")
	writeFixtureTree(t, first)
	writeFixtureTree(t, second)
	writeFile(t, filepath.Join(first, "Makefile"), "build-peer-binaries: first\n", 0o644)
	writeFile(t, filepath.Join(second, "Makefile"), "build-peer-binaries: second\n", 0o644)

	firstRequest := fixtureRequest(first)
	firstRequest.paths = append(firstRequest.paths, filepath.Join(first, "Makefile"))
	secondRequest := fixtureRequest(second)
	secondRequest.paths = append(secondRequest.paths, filepath.Join(second, "Makefile"))

	writeFile(t, filepath.Join(first, "bin", "tool"), "first artifact", 0o755)
	if err := save(firstRequest); err != nil {
		t.Fatal(err)
	}
	hit, err := restore(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("expected shared cache miss when only Makefile content differs")
	}
}

func TestRestoreRejectsCorruptSharedArtifact(t *testing.T) {
	repository := initRepository(t)
	worktree := addWorktree(t, repository, "corrupt")
	writeFixtureTree(t, worktree)
	request := fixtureRequest(worktree)
	writeFile(t, filepath.Join(worktree, "bin", "tool"), "artifact", 0o755)
	if err := save(request); err != nil {
		t.Fatal(err)
	}
	key, _, err := requestKey(request)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := cacheEntry(worktree, request.name, key)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(entry, "artifact.tar.gz"), "corrupt", 0o600)
	if _, err := restore(request); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("restore error = %v", err)
	}
}

func TestRequestKeyRejectsOutputThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFixtureTree(t, root)
	if err := os.Symlink(outside, filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	request := fixtureRequest(root)
	if _, _, err := requestKey(request); err == nil || !strings.Contains(err.Error(), "must not traverse symlink") {
		t.Fatalf("request key error = %v", err)
	}
}

func TestConcurrentSavePublishesOneValidEntry(t *testing.T) {
	repository := initRepository(t)
	first := addWorktree(t, repository, "writer-one")
	second := addWorktree(t, repository, "writer-two")
	writeFixtureTree(t, first)
	writeFixtureTree(t, second)
	writeFile(t, filepath.Join(first, "bin", "tool"), "same artifact", 0o755)
	writeFile(t, filepath.Join(second, "bin", "tool"), "same artifact", 0o755)

	requests := []cacheRequest{fixtureRequest(first), fixtureRequest(second)}
	for index := range requests {
		request := requests[index]
		t.Run(filepath.Base(request.root), func(t *testing.T) {
			t.Parallel()
			if err := save(request); err != nil {
				t.Fatal(err)
			}
			hit, err := restore(request)
			if err != nil {
				t.Fatal(err)
			}
			if !hit {
				t.Fatal("expected published cache entry")
			}
		})
	}
}

func TestSavePrunesOldEntriesPerPhase(t *testing.T) {
	repository := initRepository(t)
	worktree := addWorktree(t, repository, "prune")
	writeFixtureTree(t, worktree)
	request := fixtureRequest(worktree)
	for index := range maxCacheEntriesPerPhase + 3 {
		request.inputs = []string{"variant=" + string(rune('a'+index))}
		writeFile(t, filepath.Join(worktree, "bin", "tool"), request.inputs[0], 0o755)
		if err := save(request); err != nil {
			t.Fatal(err)
		}
	}
	key, _, err := requestKey(request)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := cacheEntry(worktree, request.name, key)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := cacheEntryAges(filepath.Dir(entry), readDirectory(t, filepath.Dir(entry)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxCacheEntriesPerPhase {
		t.Fatalf("cache entries = %d, limit = %d", len(entries), maxCacheEntriesPerPhase)
	}
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("latest cache entry was pruned: %v", err)
	}
}

func newCacheFixture(t *testing.T, name string) cacheRequest {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	writeFixtureTree(t, root)
	return fixtureRequest(root)
}

func fixtureRequest(root string) cacheRequest {
	return cacheRequest{
		action:  "save",
		root:    root,
		name:    "go-binaries",
		inputs:  []string{"GOOS=test", "GOARCH=test"},
		paths:   []string{filepath.Join(root, "source")},
		outputs: []string{filepath.Join(root, "bin", "tool")},
	}
}

func writeFixtureTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "source", "main.go"), "package main\n", 0o644)
}

func initRepository(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repository")
	runGit(t, "", "init", root)
	writeFile(t, filepath.Join(root, "tracked.txt"), "tracked\n", 0o644)
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "-c", "user.name=Cache Test", "-c", "user.email=cache@example.invalid", "commit", "-m", "初始化")
	return root
}

func addWorktree(t *testing.T, repository, name string) string {
	t.Helper()
	worktree := filepath.Join(filepath.Dir(repository), name)
	runGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	return worktree
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	if directory != "" {
		command.Dir = directory
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readDirectory(t *testing.T, path string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
