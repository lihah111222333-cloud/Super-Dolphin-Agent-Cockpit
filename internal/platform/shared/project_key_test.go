package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func TestProjectKeyFromCwdPrefersCanonicalGitRoot(t *testing.T) {
	t.Parallel()

	repo := filepath.Join(t.TempDir(), "worktrees", "tag-v1.14")
	if err := os.MkdirAll(filepath.Join(repo, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	cmd := exec.Command("git", "init", repo)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, string(output))
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("EvalSymlinks(repo): %v", err)
	}

	if got, want := ProjectKeyFromCwd(filepath.Join(repo, "nested")), "worktrees_tag-v1.14"; got != want {
		t.Fatalf("ProjectKeyFromCwd(git subdir) = %q, want %q", got, want)
	}
	if got, want := MemoryProjectKeyFromCwd(filepath.Join(repo, "nested")), headMemoryProjectKeyOracle(canonicalRepo); got != want {
		t.Fatalf("MemoryProjectKeyFromCwd(git subdir) = %q, want %q", got, want)
	}
}

func TestProjectKeyFromCwdFallsBackToAbsolutePath(t *testing.T) {
	t.Parallel()

	cwd := filepath.Join(t.TempDir(), "wj", "super-agent-v3")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	if got, want := ProjectKeyFromCwd(cwd), "wj_super-agent-v3"; got != want {
		t.Fatalf("ProjectKeyFromCwd(non-git) = %q, want %q", got, want)
	}
	if got, want := MemoryProjectKeyFromCwd(cwd), headMemoryProjectKeyOracle(cwd); got != want {
		t.Fatalf("MemoryProjectKeyFromCwd(non-git) = %q, want %q", got, want)
	}
}

func TestSanitizeMemoryProjectKey_MatchesHeadBaseline(t *testing.T) {
	t.Parallel()

	cases := []string{
		"/Users/mima0000/Desktop/wj/super-agent-v3",
		"/Users/x/y",
		"",
		"/Users/Mima 0000/Desktop/wj/超级-agent",
		"/tmp/___",
		"/Volumes/bot/super-agent-v3",
		"/Users/mima0000/Desktop/wj/" + strings.Repeat("long-segment-", 12) + "repo",
	}
	for _, raw := range cases {
		if got, want := SanitizeMemoryProjectKey(raw), headMemoryProjectKeyOracle(raw); got != want {
			t.Fatalf("SanitizeMemoryProjectKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSanitizeSkillProjectKey_MatchesDiskNames(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/Users/mima0000":                      "Users_mima0000",
		"/Users/mima0000/Desktop/wj/langgraph": "wj_langgraph",
		"/Volumes/bot/super-agent-v3":          "bot_super-agent-v3",
		"/Users/mima0000/Desktop/wj/go-agent-v2/cmd/agent-terminal/frontend": "agent-terminal_frontend",
		"/Users/mima0000/Desktop/wj/super-agent-v3":                          "wj_super-agent-v3",
	}
	for raw, want := range cases {
		if got := SanitizeSkillProjectKey(raw); got != want {
			t.Fatalf("SanitizeSkillProjectKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func headMemoryProjectKeyOracle(raw string) string {
	const maxLen = 96

	normalized := filepath.ToSlash(norm.NFC.String(strings.TrimSpace(raw)))
	var builder strings.Builder
	lastDash := false
	for _, r := range normalized {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case lastDash:
		default:
			builder.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "project-" + shortHashOracle(normalized)
	}
	if len(slug) <= maxLen {
		return slug
	}
	prefix := strings.Trim(slug[:maxLen-9], "-")
	if prefix == "" {
		prefix = "project"
	}
	return prefix + "-" + shortHashOracle(normalized)
}

func shortHashOracle(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:4])
}
