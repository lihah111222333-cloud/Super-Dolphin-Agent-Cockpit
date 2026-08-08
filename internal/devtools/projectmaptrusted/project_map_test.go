package projectmaptrusted

import (
	"archive/tar"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedTreeRunsTrustedGeneratorWithoutGitMetadata(t *testing.T) {
	repository := newTrustedProjectMapRepository(t)
	tree := trustedProjectMapGit(t, repository, "write-tree")
	prepared, err := prepareTree(repository, tree)
	if err != nil {
		t.Fatalf("prepare archived tree: %v", err)
	}
	t.Cleanup(func() {
		if err := prepared.cleanup(); err != nil {
			t.Errorf("clean prepared tree: %v", err)
		}
	})

	if _, err := os.Lstat(filepath.Join(prepared.sourceRoot, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archived source contains Git metadata: %v", err)
	}
	if pathWithin(prepared.sourceRoot, prepared.generatorPath) {
		t.Fatalf("trusted generator was installed inside candidate source: %s", prepared.generatorPath)
	}

	var stdout bytes.Buffer
	if err := runTrustedGenerator(prepared, false, &stdout); err != nil {
		t.Fatalf("run trusted generator in archived tree: %v", err)
	}
	manifest := filepath.Join(prepared.sourceRoot, filepath.FromSlash(managedOutputPath), "AI_PROJECT_MANIFEST.json")
	if info, err := os.Stat(manifest); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("trusted generator did not create manifest: %v", err)
	}
	for _, marker := range []string{"candidate-generator-executed", "candidate-make-executed"} {
		if _, err := os.Lstat(filepath.Join(prepared.sourceRoot, marker)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("candidate entrypoint created %s: %v", marker, err)
		}
	}
}

func TestMaterializeExactTreeRejectsRelativeTMPDIRWithoutRepositoryLeak(t *testing.T) {
	repository := newTrustedProjectMapRepository(t)
	tree := trustedProjectMapGit(t, repository, "write-tree")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(repository); err != nil {
		t.Fatalf("enter fixture repository: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previous); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	t.Setenv("TMPDIR", "var/project-map-relative-temp")
	if _, err := MaterializeExactTree(repository, tree, "relative-tmpdir-"); err == nil || !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("MaterializeExactTree() error = %v, want absolute TMPDIR rejection", err)
	}
	if _, err := os.Stat(filepath.Join(repository, "var")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("relative TMPDIR leaked into repository: %v", err)
	}
}

func TestExtractGitTreeDrainsArchivePaddingBeforeWait(t *testing.T) {
	repository := newTrustedProjectMapRepository(t)
	tree := trustedProjectMapGit(t, repository, "write-tree")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locate real git: %v", err)
	}
	fakeBin := t.TempDir()
	fakeGit := filepath.Join(fakeBin, "git")
	script := "#!/bin/sh\n\"$PROJECT_MAP_REAL_GIT\" \"$@\"\nstatus=$?\nif [ \"$status\" -ne 0 ]; then exit \"$status\"; fi\n/bin/dd if=/dev/zero bs=1024 count=128 2>/dev/null\n"
	if err := os.WriteFile(fakeGit, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", fakeBin)
	t.Setenv("PROJECT_MAP_REAL_GIT", realGit)

	destination := filepath.Join(t.TempDir(), "source")
	if err := extractGitTree(repository, tree, destination); err != nil {
		t.Fatalf("extractGitTree() with trailing archive padding: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "README.md")); err != nil {
		t.Fatalf("extractGitTree() did not materialize archive: %v", err)
	}
}

func TestRefreshTreeOverwritesDirtyManagedOutputsOnly(t *testing.T) {
	repository := newTrustedProjectMapRepository(t)
	tree := trustedProjectMapGit(t, repository, "write-tree")
	writeTrustedProjectMapTestFile(t, repository, managedOutputPath+"/AI_PROJECT_MAP.md", "dirty generated output\n")
	writeTrustedProjectMapTestFile(t, repository, managedOutputPath+"/untracked.md", "untracked generated output\n")
	writeTrustedProjectMapTestFile(t, repository, "untracked-user.txt", "user work\n")

	var stdout bytes.Buffer
	if err := RefreshTree(repository, tree, &stdout); err != nil {
		t.Fatalf("refresh tree with dirty managed outputs: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(repository, filepath.FromSlash(managedOutputPath), "untracked.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("untracked managed output survived refresh: %v", err)
	}
	if info, err := os.Stat(filepath.Join(repository, filepath.FromSlash(managedOutputPath), "AI_PROJECT_MANIFEST.json")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("refresh did not write generated manifest: %v", err)
	}
	assertTrustedProjectMapTestFile(t, repository, "untracked-user.txt", "user work\n")
}

func TestExtractArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name        string
		header      func(root string) tar.Header
		wantErrText string
	}{
		{
			name: "parent traversal",
			header: func(string) tar.Header {
				return tar.Header{Name: "../escape", Typeflag: tar.TypeReg, Mode: 0o600}
			},
			wantErrText: "unsafe path",
		},
		{
			name: "normalized traversal",
			header: func(string) tar.Header {
				return tar.Header{Name: "nested/../../escape", Typeflag: tar.TypeReg, Mode: 0o600}
			},
			wantErrText: "unsafe path",
		},
		{
			name: "absolute path",
			header: func(root string) tar.Header {
				return tar.Header{Name: filepath.ToSlash(filepath.Join(root, "escape")), Typeflag: tar.TypeReg, Mode: 0o600}
			},
			wantErrText: "unsafe path",
		},
		{
			name: "symbolic link",
			header: func(string) tar.Header {
				return tar.Header{Name: "safe-link", Typeflag: tar.TypeSymlink, Linkname: "../escape", Mode: 0o777}
			},
			wantErrText: "forbidden type",
		},
		{
			name: "hard link",
			header: func(string) tar.Header {
				return tar.Header{Name: "safe-hard-link", Typeflag: tar.TypeLink, Linkname: "../escape", Mode: 0o600}
			},
			wantErrText: "forbidden type",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "source")
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatalf("create archive destination: %v", err)
			}
			header := test.header(root)
			err := extractArchive(bytes.NewReader(trustedProjectMapArchive(t, header, []byte("payload"))), destination)
			if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
				t.Fatalf("extractArchive() error = %v, want text %q", err, test.wantErrText)
			}
			if _, err := os.Lstat(filepath.Join(root, "escape")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsafe archive entry escaped destination: %v", err)
			}
		})
	}
}

func TestReplaceManagedOutputsOnlyWritesManagedDirectory(t *testing.T) {
	generated := t.TempDir()
	writeTrustedProjectMapTestFile(t, generated, "AI_PROJECT_MAP.md", "generated map\n")
	writeTrustedProjectMapTestFile(t, generated, "AI_PROJECT_MANIFEST.json", "{\"generated\":true}\n")
	writeTrustedProjectMapTestFile(t, generated, "index/modules.tsv", "module\tpath\n")

	repository := t.TempDir()
	writeTrustedProjectMapTestFile(t, repository, "README.md", "user root file\n")
	writeTrustedProjectMapTestFile(t, repository, "docs/doc/codemap/README.md", "adjacent user file\n")
	writeTrustedProjectMapTestFile(t, repository, managedOutputPath+"/stale.md", "stale output\n")

	if err := replaceManagedOutputs(generated, repository); err != nil {
		t.Fatalf("replace managed outputs: %v", err)
	}
	assertTrustedProjectMapTestFile(t, repository, "README.md", "user root file\n")
	assertTrustedProjectMapTestFile(t, repository, "docs/doc/codemap/README.md", "adjacent user file\n")
	assertTrustedProjectMapTestFile(t, repository, managedOutputPath+"/AI_PROJECT_MAP.md", "generated map\n")
	assertTrustedProjectMapTestFile(t, repository, managedOutputPath+"/AI_PROJECT_MANIFEST.json", "{\"generated\":true}\n")
	assertTrustedProjectMapTestFile(t, repository, managedOutputPath+"/index/modules.tsv", "module\tpath\n")
	if _, err := os.Lstat(filepath.Join(repository, filepath.FromSlash(managedOutputPath), "stale.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale managed output survived refresh: %v", err)
	}
}

func TestReplaceManagedOutputsRejectsSymlinkedManagedDirectory(t *testing.T) {
	generated := t.TempDir()
	writeTrustedProjectMapTestFile(t, generated, "AI_PROJECT_MANIFEST.json", "{}\n")

	repository := t.TempDir()
	parent := filepath.Join(repository, "docs", "doc", "codemap")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("create managed output parent: %v", err)
	}
	outside := t.TempDir()
	writeTrustedProjectMapTestFile(t, outside, "sentinel.txt", "outside\n")
	if err := os.Symlink(outside, filepath.Join(parent, "project-map")); err != nil {
		t.Fatalf("create managed output symlink: %v", err)
	}

	if err := replaceManagedOutputs(generated, repository); err == nil {
		t.Fatal("replaceManagedOutputs() accepted a symlinked managed directory")
	}
	assertTrustedProjectMapTestFile(t, outside, "sentinel.txt", "outside\n")
	if _, err := os.Lstat(filepath.Join(outside, "AI_PROJECT_MANIFEST.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refresh wrote through managed output symlink: %v", err)
	}
}

func newTrustedProjectMapRepository(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	trustedProjectMapGit(t, repository, "init", "-q")
	trustedProjectMapGit(t, repository, "config", "user.name", "Trusted Project Map Test")
	trustedProjectMapGit(t, repository, "config", "user.email", "project-map-trusted@example.invalid")

	files := map[string]string{
		".ai-project-map.overrides.json":      `{"drift_thresholds_patch":{"max_unknown_ratio":1}}` + "\n",
		"AGENTS.md":                           "Use docs/adr/*.md as current decisions.\n",
		"CLAUDE.md":                           "Trusted project-map fixture.\n",
		"Makefile":                            "$(shell touch candidate-make-executed)\nall:\n\t@false\n",
		"README.md":                           "Trusted project-map fixture.\n",
		"docs/README.md":                      "Current documentation.\n",
		"docs/adr/current.md":                 "Current ADR.\n",
		"docs/archive/reviews/old.md":         "Historical review.\n",
		"docs/work/plans/current.md":          "Current plan.\n",
		"docs/契约/README.md":                   "Architecture decisions live in docs/adr.\n",
		"docs/契约/fix-workflow-convention.md":  "docs/work/plans/\ndocs/archive/reviews/\ndocs/adr/\n",
		"docs/契约/mcp-service-convention.md":   "Current MCP service convention.\n",
		"go.mod":                              "module example.invalid/project-map-trusted\n\ngo 1.26.5\n",
		"scripts/codemap_policy.txt":          trustedProjectMapPolicy(),
		"scripts/generate_ai_project_map.mjs": "import fs from 'node:fs';\nfs.writeFileSync('candidate-generator-executed', 'executed');\n",
	}
	for relative, content := range files {
		writeTrustedProjectMapTestFile(t, repository, relative, content)
	}
	trustedProjectMapGit(t, repository, "add", "-A")
	return repository
}

func trustedProjectMapPolicy() string {
	return strings.Join([]string{
		"schema\t1",
		"historical\tdocs/archive",
		"shard\tapp-ui\tapp-ui.tsv",
		"shard\torchestration\torchestration.tsv",
		"shard\tmodules\tmodules.tsv",
		"shard\tplatform-provider\tplatform-provider.tsv",
		"shard\tstore-sql\tstore-sql.tsv",
		"shard\tremote-ci\tremote-ci.tsv",
		"shard\tdocs-agent\tdocs-agent.tsv",
		"shard\tother\tother.tsv",
		"",
	}, "\n")
}

func trustedProjectMapGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", repository}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func trustedProjectMapArchive(t *testing.T, header tar.Header, content []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if header.Typeflag == tar.TypeReg || header.Typeflag == 0 {
		header.Size = int64(len(content))
	}
	if err := writer.WriteHeader(&header); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if header.Size > 0 {
		if _, err := writer.Write(content); err != nil {
			t.Fatalf("write tar content: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return archive.Bytes()
}

func writeTrustedProjectMapTestFile(t *testing.T, root, relative, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", relative, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relative, err)
	}
}

func assertTrustedProjectMapTestFile(t *testing.T, root, relative, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", relative, got, want)
	}
}
