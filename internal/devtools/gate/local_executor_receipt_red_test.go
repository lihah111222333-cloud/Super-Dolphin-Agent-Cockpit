package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLocalReceiptRunnerClosureTracksExactTreeReachableSources(t *testing.T) {
	trustedGit, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("trusted Git is unavailable: %v", err)
	}
	files := localRunnerClosureFixtureFiles()
	firstRoot := newLocalRunnerClosureFixture(t, files)
	firstTree := writeLocalRunnerClosureTree(t, firstRoot)
	paths, baseline := localRunnerExactSourceDigest(t, trustedGit, firstRoot, firstTree)
	for _, sourcePath := range []string{
		"cmd/super-dolphin-gate/local_test_cli.go",
		"internal/devtools/gate/local_executor.go",
		"internal/devtools/remoteci/local_workload_plan.go",
		"internal/devtools/projectmaptrusted/project_map.go",
		"internal/devtools/cicontract/local_pass_contract.go",
	} {
		if !slices.Contains(paths, sourcePath) {
			t.Fatalf("dynamic runner closure omitted reachable source %q: %v", sourcePath, paths)
		}
		mustLocalExecutorWriteFile(t, filepath.Join(firstRoot, sourcePath), files[sourcePath]+"\n// semantic drift\n")
		driftedTree := writeLocalRunnerClosureTree(t, firstRoot)
		_, drifted := localRunnerExactSourceDigest(t, trustedGit, firstRoot, driftedTree)
		if drifted == baseline {
			t.Fatalf("runner semantic digest ignored reachable source drift %q", sourcePath)
		}
		mustLocalExecutorWriteFile(t, filepath.Join(firstRoot, sourcePath), files[sourcePath])
		writeLocalRunnerClosureTree(t, firstRoot)
	}
	unrelatedPath := filepath.Join(firstRoot, "internal", "unrelated", "unrelated.go")
	if err := os.MkdirAll(filepath.Dir(unrelatedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mustLocalExecutorWriteFile(t, unrelatedPath, "package unrelated\n")
	unrelatedTree := writeLocalRunnerClosureTree(t, firstRoot)
	_, unrelated := localRunnerExactSourceDigest(t, trustedGit, firstRoot, unrelatedTree)
	if unrelated != baseline {
		t.Fatalf("runner semantic digest changed for unreachable source: %q != %q", unrelated, baseline)
	}
	secondRoot := newLocalRunnerClosureFixture(t, files)
	secondTree := writeLocalRunnerClosureTree(t, secondRoot)
	_, second := localRunnerExactSourceDigest(t, trustedGit, secondRoot, secondTree)
	if second != baseline {
		t.Fatalf("runner semantic digest depends on repository root: %q != %q", second, baseline)
	}
}

func TestLocalReceiptRunnerClosureRejectsMissingAndNonBlobSources(t *testing.T) {
	trustedGit, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("trusted Git is unavailable: %v", err)
	}
	files := localRunnerClosureFixtureFiles()
	root := newLocalRunnerClosureFixture(t, files)
	writeLocalRunnerClosureTree(t, root)
	missing := strings.Replace(files["cmd/super-dolphin-gate/local_test_cli.go"], "\t_ \"example.invalid/local-runner/internal/devtools/remoteci\"\n", "\t_ \"example.invalid/local-runner/internal/devtools/remoteci\"\n\t_ \"example.invalid/local-runner/internal/missing\"\n", 1)
	mustLocalExecutorWriteFile(t, filepath.Join(root, "cmd/super-dolphin-gate/local_test_cli.go"), missing)
	missingTree := writeLocalRunnerClosureTree(t, root)
	if _, err := gitTreeLocalRunnerSourcePaths(context.Background(), trustedGit, root, missingTree); err == nil {
		t.Fatal("runner closure accepted a missing imported production package")
	}
	mustLocalExecutorWriteFile(t, filepath.Join(root, "cmd/super-dolphin-gate/local_test_cli.go"), files["cmd/super-dolphin-gate/local_test_cli.go"])
	invalid := strings.Replace(files["cmd/super-dolphin-gate/local_test_cli.go"], "\t_ \"example.invalid/local-runner/internal/devtools/remoteci\"\n", "\t_ \"example.invalid/local-runner/../escape\"\n", 1)
	mustLocalExecutorWriteFile(t, filepath.Join(root, "cmd/super-dolphin-gate/local_test_cli.go"), invalid)
	invalidTree := writeLocalRunnerClosureTree(t, root)
	if _, err := gitTreeLocalRunnerSourcePaths(context.Background(), trustedGit, root, invalidTree); err == nil {
		t.Fatal("runner closure accepted an invalid local import path")
	}
	mustLocalExecutorWriteFile(t, filepath.Join(root, "cmd/super-dolphin-gate/local_test_cli.go"), files["cmd/super-dolphin-gate/local_test_cli.go"])
	if err := os.Symlink("local_executor.go", filepath.Join(root, "internal/devtools/gate/non_blob.go")); err != nil {
		t.Fatal(err)
	}
	nonBlobTree := writeLocalRunnerClosureTree(t, root)
	if _, err := gitTreeLocalRunnerSourcePaths(context.Background(), trustedGit, root, nonBlobTree); err == nil {
		t.Fatal("runner closure accepted a non-blob production source")
	}
}

func TestLocalReceiptRunnerClosureTracksReachableEmbedAssets(t *testing.T) {
	trustedGit, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("trusted Git is unavailable: %v", err)
	}
	files := localRunnerClosureFixtureFiles()
	firstRoot := newLocalRunnerClosureFixture(t, files)
	firstTree := writeLocalRunnerClosureTree(t, firstRoot)
	paths, baseline := localRunnerExactSourceDigest(t, trustedGit, firstRoot, firstTree)
	assetPath := "internal/devtools/projectmaptrusted/assets/generate_ai_project_map.mjs.gz"
	if !slices.Contains(paths, assetPath) {
		t.Fatalf("dynamic runner closure omitted reachable embed asset %q: %v", assetPath, paths)
	}
	mustLocalExecutorWriteFile(t, filepath.Join(firstRoot, assetPath), "embed asset drift\n")
	driftedTree := writeLocalRunnerClosureTree(t, firstRoot)
	_, drifted := localRunnerExactSourceDigest(t, trustedGit, firstRoot, driftedTree)
	if drifted == baseline {
		t.Fatalf("runner semantic digest ignored reachable embed asset drift %q", assetPath)
	}
	if err := os.MkdirAll(filepath.Join(firstRoot, "internal/unrelated/assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustLocalExecutorWriteFile(t, filepath.Join(firstRoot, "internal/unrelated/assets/ignored.gz"), "unreachable asset\n")
	unrelatedTree := writeLocalRunnerClosureTree(t, firstRoot)
	_, unrelated := localRunnerExactSourceDigest(t, trustedGit, firstRoot, unrelatedTree)
	if unrelated != drifted {
		t.Fatalf("runner semantic digest changed for unreachable embed-like asset: %q != %q", unrelated, drifted)
	}
	secondRoot := newLocalRunnerClosureFixture(t, localRunnerClosureFixtureFiles())
	mustLocalExecutorWriteFile(t, filepath.Join(secondRoot, assetPath), "embed asset drift\n")
	secondTree := writeLocalRunnerClosureTree(t, secondRoot)
	_, second := localRunnerExactSourceDigest(t, trustedGit, secondRoot, secondTree)
	if second != drifted {
		t.Fatalf("runner semantic digest depends on repository root for equal embed assets: %q != %q", second, drifted)
	}
}

func TestLocalReceiptRunnerClosureRejectsInvalidEmbedAssets(t *testing.T) {
	trustedGit, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("trusted Git is unavailable: %v", err)
	}
	files := localRunnerClosureFixtureFiles()
	root := newLocalRunnerClosureFixture(t, files)
	projectMapPath := filepath.Join(root, "internal/devtools/projectmaptrusted/project_map.go")
	for _, testCase := range []struct {
		name    string
		content string
	}{
		{name: "no match", content: strings.Replace(files["internal/devtools/projectmaptrusted/project_map.go"], "assets/*.gz", "assets/*.missing", 1)},
		{name: "path escape", content: strings.Replace(files["internal/devtools/projectmaptrusted/project_map.go"], "assets/*.gz", "../assets/*.gz", 1)},
		{name: "invalid pattern", content: strings.Replace(files["internal/devtools/projectmaptrusted/project_map.go"], "assets/*.gz", "assets/[", 1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mustLocalExecutorWriteFile(t, projectMapPath, testCase.content)
			tree := writeLocalRunnerClosureTree(t, root)
			if _, err := gitTreeLocalRunnerSourcePaths(context.Background(), trustedGit, root, tree); err == nil {
				t.Fatal("runner closure accepted invalid reachable embed directive")
			}
		})
	}
	mustLocalExecutorWriteFile(t, projectMapPath, files["internal/devtools/projectmaptrusted/project_map.go"])
	assetPath := filepath.Join(root, "internal/devtools/projectmaptrusted/assets/generate_ai_project_map.mjs.gz")
	if err := os.Remove(assetPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../project_map.go", assetPath); err != nil {
		t.Fatal(err)
	}
	symlinkTree := writeLocalRunnerClosureTree(t, root)
	if _, err := gitTreeLocalRunnerSourcePaths(context.Background(), trustedGit, root, symlinkTree); err == nil {
		t.Fatal("runner closure accepted a symlink embed asset")
	}
}

func TestLocalReceiptRunnerClosureDeduplicatesEmbeddedProductionSource(t *testing.T) {
	trustedGit, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("trusted Git is unavailable: %v", err)
	}
	files := localRunnerClosureFixtureFiles()
	files["internal/devtools/projectmaptrusted/a_embed.go"] = "package projectmaptrusted\n\nimport _ \"embed\"\n\n//go:embed project_map.go\nvar embeddedProductionSource []byte\n"
	root := newLocalRunnerClosureFixture(t, files)
	tree := writeLocalRunnerClosureTree(t, root)
	paths, baseline := localRunnerExactSourceDigest(t, trustedGit, root, tree)
	projectMapPath := "internal/devtools/projectmaptrusted/project_map.go"
	if count := localRunnerPathCount(paths, projectMapPath); count != 1 {
		t.Fatalf("embedded production source occurrence count = %d, want 1: %v", count, paths)
	}
	mustLocalExecutorWriteFile(t, filepath.Join(root, projectMapPath), files[projectMapPath]+"\n// source drift\n")
	driftedTree := writeLocalRunnerClosureTree(t, root)
	_, drifted := localRunnerExactSourceDigest(t, trustedGit, root, driftedTree)
	if drifted == baseline {
		t.Fatal("deduplicated embedded production source drift did not change closure digest")
	}
}

func localRunnerPathCount(paths []string, target string) int {
	count := 0
	for _, value := range paths {
		if value == target {
			count++
		}
	}
	return count
}

func TestAppendLocalRunnerClosurePathRejectsContentCollision(t *testing.T) {
	owners := make(map[string]localRunnerClosurePathOwner)
	paths, err := appendLocalRunnerClosurePath(nil, owners, "fixture/source.go", []byte("first"), "production source")
	if err != nil {
		t.Fatal(err)
	}
	paths, err = appendLocalRunnerClosurePath(paths, owners, "fixture/source.go", []byte("first"), "go:embed asset")
	if err != nil || !slices.Equal(paths, []string{"fixture/source.go"}) {
		t.Fatalf("equal content duplicate = paths:%v err:%v, want one path and no error", paths, err)
	}
	if _, err := appendLocalRunnerClosurePath(paths, owners, "fixture/source.go", []byte("different"), "go:embed asset"); err == nil || !strings.Contains(err.Error(), "content collision") {
		t.Fatalf("different-content duplicate error = %v, want content collision", err)
	}
}

func TestParseGitTreeLocalRunnerPackageEntriesAcceptsTrailingTerminator(t *testing.T) {
	tests := []struct {
		name   string
		output []byte
		want   []string
	}{
		{name: "trailing terminator", output: []byte("100644 blob abcdef\tinternal/devtools/projectmaptrusted/assets/project-map.gz\x00"), want: []string{"100644 blob abcdef\tinternal/devtools/projectmaptrusted/assets/project-map.gz"}},
		{name: "no matches", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseGitTreeLocalRunnerPackageEntries(test.output)
			if err != nil {
				t.Fatalf("parse Git tree entries: %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("entries = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseGitTreeLocalRunnerPackageEntriesRejectsMalformedEmptyEntry(t *testing.T) {
	valid := "100644 blob abcdef\tinternal/devtools/projectmaptrusted/assets/project-map.gz"
	for _, test := range []struct {
		output  []byte
		message string
	}{
		{output: []byte(valid), message: "missing NUL terminator"},
		{output: []byte(valid + "\x00\x00" + valid + "\x00"), message: "tree entry"},
		{output: []byte("\x00"), message: "tree entry"},
	} {
		if _, err := parseGitTreeLocalRunnerPackageEntries(test.output); err == nil || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("parse malformed Git tree entries error = %v, want %q", err, test.message)
		}
	}
}

func localRunnerExactSourceDigest(t *testing.T, trustedGit TrustedGitBinary, root, tree string) ([]string, string) {
	t.Helper()
	sources, err := readLocalReceiptRunnerSources(context.Background(), trustedGit, root, tree)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(sources))
	for _, source := range sources {
		paths = append(paths, source.path)
	}
	digest, err := localRunnerSemanticSourceDigest(sources, nil)
	if err != nil {
		t.Fatal(err)
	}
	return paths, digest
}

func newLocalRunnerClosureFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for sourcePath, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, sourcePath)), 0o700); err != nil {
			t.Fatal(err)
		}
		mustLocalExecutorWriteFile(t, filepath.Join(root, sourcePath), content)
	}
	localRunnerFixtureGit(t, root, "init", "--quiet")
	return root
}

func writeLocalRunnerClosureTree(t *testing.T, root string) string {
	t.Helper()
	localRunnerFixtureGit(t, root, "add", "-A")
	return strings.TrimSpace(localRunnerFixtureGit(t, root, "write-tree"))
}

func localRunnerFixtureGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func localRunnerClosureFixtureFiles() map[string]string {
	return map[string]string{
		"go.mod": "module example.invalid/local-runner\n\ngo 1.25\n",
		"cmd/super-dolphin-gate/local_test_cli.go":                                  "package main\n\nimport (\n\t_ \"example.invalid/local-runner/internal/devtools/gate\"\n\t_ \"example.invalid/local-runner/internal/devtools/projectmaptrusted\"\n\t_ \"example.invalid/local-runner/internal/devtools/remoteci\"\n)\n",
		"internal/devtools/gate/local_executor.go":                                  "package gate\n\nimport _ \"example.invalid/local-runner/internal/devtools/cicontract\"\n",
		"internal/devtools/remoteci/local_workload_plan.go":                         "package remoteci\n\nimport _ \"example.invalid/local-runner/internal/devtools/gate\"\n",
		"internal/devtools/projectmaptrusted/project_map.go":                        "package projectmaptrusted\n\nimport (\n\t_ \"embed\"\n\t_ \"example.invalid/local-runner/internal/devtools/gate\"\n)\n\n//go:embed assets/*.gz\nvar projectMapAssets []byte\n",
		"internal/devtools/projectmaptrusted/assets/generate_ai_project_map.mjs.gz": "embedded asset\n",
		"internal/devtools/cicontract/local_pass_contract.go":                       "package cicontract\n",
	}
}

func TestLocalReceiptToolDiscoveryDoesNotUseCallerPATH(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	fake := filepath.Join(root, "go")
	mustLocalExecutorWriteFile(t, fake, "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	resolved, err := findTrustedReceiptTool("go")
	if err != nil {
		t.Skipf("host has no Go in fixed receipt directories: %v", err)
	}
	if resolved == fake || strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		t.Fatalf("receipt tool discovery accepted caller PATH fake %q", resolved)
	}
}

func TestTrustedGitBinaryDoesNotUseCallerPATHAndRejectsDigestDrift(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	marker := filepath.Join(root, "fake-git-ran")
	fake := filepath.Join(root, "git")
	mustLocalExecutorWriteFile(t, fake, "#!/bin/sh\ntouch \"$LOCAL_RECEIPT_FAKE_GIT_MARKER\"\nexit 0\n")
	if err := os.Chmod(fake, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("LOCAL_RECEIPT_FAKE_GIT_MARKER", marker)
	trusted, err := ResolveTrustedGitBinary(context.Background())
	if err != nil {
		t.Skipf("host has no Git in fixed receipt directories: %v", err)
	}
	if _, err := trusted.VerifiedPath(); err != nil {
		t.Fatalf("verify trusted Git: %v", err)
	}
	if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("trusted Git resolution executed caller PATH fake: %v", err)
	}
	trusted.digest = digestForWorkloadPass("drifted-git")
	if _, err := trusted.VerifiedPath(); err == nil || !strings.Contains(err.Error(), "content drifted") {
		t.Fatalf("trusted Git digest drift error = %v, want content drift rejection", err)
	}
}

func TestLocalReceiptReverifyRejectsDependencyContentDrift(t *testing.T) {
	root := canonicalLocalSandboxTempDir(t)
	mustLocalExecutorWriteFile(t, filepath.Join(root, "changed-content"), "drift")
	proof := localExecutorDependencyProof{name: "test", root: root, contentDigest: digestForWorkloadPass("old-content"), contentFiles: []localExecutorDependencyContentFile{{path: "changed-content", digest: digestForWorkloadPass("old-content"), mode: 0o600}}}
	if err := reverifyLocalReceiptDependency(root, proof); err == nil {
		t.Fatal("reverify accepted dependency content drift")
	}
}

func TestLocalExecutorSupportRejectsUnknownAndEmptyStrategies(t *testing.T) {
	for _, program := range []ExecutorProgram{
		{},
		{Strategy: ExecutorStrategy("unknown")},
	} {
		if err := validateLocalExecutorProgramSupport(program); err == nil {
			t.Fatalf("program %#v unexpectedly eligible", program)
		}
	}
}
