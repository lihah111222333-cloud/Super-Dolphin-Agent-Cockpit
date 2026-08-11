package gate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReviewLocalReceiptExactTreeReadsIgnoreReplaceRefs proves receipt-bound
// exact-tree reads do not follow repository replacement refs or ambient Git
// repository routing.
func TestReviewLocalReceiptExactTreeReadsIgnoreReplaceRefs(t *testing.T) {
	repository := t.TempDir()
	localReceiptTestGit(t, repository, "init", "--quiet")
	writeLocalReceiptTestFile(t, filepath.Join(repository, "identity.txt"), "original\n")
	localReceiptTestGit(t, repository, "add", ".")
	originalTree := localReceiptGitOutput(t, repository, "write-tree")
	writeLocalReceiptTestFile(t, filepath.Join(repository, "identity.txt"), "replacement\n")
	localReceiptTestGit(t, repository, "add", ".")
	replacementTree := localReceiptGitOutput(t, repository, "write-tree")
	localReceiptTestGit(t, repository, "update-ref", "refs/replace/"+originalTree, replacementTree)

	trustedGit := localReceiptTestTrustedGit(t)
	assertLocalReceiptExactTreeContent(t, trustedGit, repository, originalTree)
	setLocalReceiptAmbientGitRouting(t)
	assertLocalReceiptExactTreeContent(t, trustedGit, repository, originalTree)
	assertLocalReceiptExactTreeRegularFiles(t, trustedGit, repository, originalTree)
}

func assertLocalReceiptExactTreeContent(t *testing.T, trustedGit TrustedGitBinary, repository, tree string) {
	t.Helper()
	gotTree, err := verifyGitTreeObject(context.Background(), trustedGit, repository, tree)
	if err != nil || gotTree != tree {
		t.Fatalf("verified tree = %q, err = %v, want %q", gotTree, err, tree)
	}
	content, err := gitTreeBlob(context.Background(), trustedGit, repository, tree, "identity.txt")
	if err != nil || string(content) != "original\n" {
		t.Fatalf("exact tree blob = %q, err = %v, want original object", content, err)
	}
}

func setLocalReceiptAmbientGitRouting(t *testing.T) {
	t.Helper()
	repository := t.TempDir()
	localReceiptTestGit(t, repository, "init", "--quiet")
	config := filepath.Join(repository, "global.gitconfig")
	if err := os.WriteFile(config, []byte("[core]\nrepositoryformatversion = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(repository, ".git"))
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(repository, ".git", "objects"))
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
}

func assertLocalReceiptExactTreeRegularFiles(t *testing.T, trustedGit TrustedGitBinary, repository, tree string) {
	t.Helper()
	files, err := gitTreeRegularFiles(context.Background(), trustedGit, repository, tree, "identity.txt")
	if err != nil || len(files) != 1 || string(files[0].content) != "original\n" {
		t.Fatalf("exact tree regular files = %#v, err = %v, want original object", files, err)
	}
}

func localReceiptGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output))
}
