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

	gotSkill, err := ProjectKeyFromCwd(filepath.Join(repo, "nested"))
	if err != nil {
		t.Fatalf("ProjectKeyFromCwd(git subdir) error = %v", err)
	}
	if got, want := gotSkill, "worktrees_tag-v1.14"; got != want {
		t.Fatalf("ProjectKeyFromCwd(git subdir) = %q, want %q", got, want)
	}
	gotMemory, err := MemoryProjectKeyFromCwd(filepath.Join(repo, "nested"))
	if err != nil {
		t.Fatalf("MemoryProjectKeyFromCwd(git subdir) error = %v", err)
	}
	if got, want := gotMemory, headMemoryProjectKeyOracle(canonicalRepo); got != want {
		t.Fatalf("MemoryProjectKeyFromCwd(git subdir) = %q, want %q", got, want)
	}
}

func TestProjectKeyFromCwdFailsFastForNonGitDirectory(t *testing.T) {
	t.Parallel()

	cwd := filepath.Join(t.TempDir(), "wj", "super-agent-v3")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	if got, err := ProjectKeyFromCwd(cwd); err == nil {
		t.Fatalf("ProjectKeyFromCwd(non-git) = %q nil error, want git root error", got)
	}
	if got, err := MemoryProjectKeyFromCwd(cwd); err == nil {
		t.Fatalf("MemoryProjectKeyFromCwd(non-git) = %q nil error, want git root error", got)
	}
}

func TestSanitizeMemoryProjectKey_MatchesHeadBaseline(t *testing.T) {
	t.Parallel()

	cases := []string{
		"/Users/alice/Desktop/wj/super-agent-v3",
		"/Users/x/y",
		"",
		"/Users/Mima 0000/Desktop/wj/超级-agent",
		"/tmp/___",
		"/Volumes/bot/super-agent-v3",
		"/Users/alice/Desktop/wj/" + strings.Repeat("long-segment-", 12) + "repo",
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
		"/Users/alice":                      "Users_alice",
		"/Users/alice/Desktop/wj/langgraph": "wj_langgraph",
		"/Volumes/bot/super-agent-v3":       "bot_super-agent-v3",
		"/Users/alice/Desktop/wj/go-agent-v2/cmd/agent-terminal/frontend": "agent-terminal_frontend",
		"/Users/alice/Desktop/wj/super-agent-v3":                          "wj_super-agent-v3",
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
