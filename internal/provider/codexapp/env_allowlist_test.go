package codexapp

import (
	"sort"
	"strings"
	"testing"
)

func TestBuildAllowlistedSpawnEnvKeepsOnlyListed(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/a",
		"USER=a",
		"CODEX_HOME=/stale/home",       // rogue — must be dropped
		"OPENAI_API_KEY=secret",        // rogue — must be dropped
		"AWS_SESSION_TOKEN=secret",     // rogue — must be dropped
		"HTTP_PROXY=http://proxy:8080", // not on allowlist
	}
	got := buildAllowlistedSpawnEnv(parent, nil)
	text := strings.Join(got, "\n")
	// Must keep.
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/a", "USER=a"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q retained, got %v", want, got)
		}
	}
	// Must drop.
	for _, rogue := range []string{"CODEX_HOME=", "OPENAI_API_KEY=", "AWS_SESSION_TOKEN=", "HTTP_PROXY="} {
		if strings.Contains(text, rogue) {
			t.Errorf("expected %q dropped, got %v", rogue, got)
		}
	}
}

func TestBuildAllowlistedSpawnEnvOverridesWin(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HOME=/home/a",
	}
	got := buildAllowlistedSpawnEnv(parent, map[string]string{
		"CODEX_HOME": "/canonicalized/home",
		"HOME":       "/override/home", // override must win even for allowlisted keys
	})
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "CODEX_HOME=/canonicalized/home") {
		t.Fatalf("CODEX_HOME override missing: %v", got)
	}
	if !strings.Contains(text, "HOME=/override/home") {
		t.Fatalf("HOME override did not win: %v", got)
	}
	if strings.Contains(text, "HOME=/home/a") {
		t.Fatalf("parent HOME should be shadowed by override: %v", got)
	}
}

func TestBuildAllowlistedSpawnEnvIsDeterministic(t *testing.T) {
	t.Parallel()
	parent := []string{"PATH=/usr/bin", "HOME=/home/a", "USER=a", "TZ=UTC"}
	a := buildAllowlistedSpawnEnv(parent, map[string]string{"CODEX_HOME": "/x"})
	b := buildAllowlistedSpawnEnv(parent, map[string]string{"CODEX_HOME": "/x"})
	if strings.Join(a, "\n") != strings.Join(b, "\n") {
		t.Fatalf("env must be deterministic: %v vs %v", a, b)
	}
	sorted := make([]string, len(a))
	copy(sorted, a)
	sort.Strings(sorted)
	if strings.Join(sorted, "\n") != strings.Join(a, "\n") {
		t.Fatalf("env should already be sorted: %v", a)
	}
}

func TestBuildAllowlistedSpawnEnvTolerantOfMalformed(t *testing.T) {
	t.Parallel()
	got := buildAllowlistedSpawnEnv([]string{
		"PATH=/usr/bin",
		"NO_EQUALS_SIGN",
		"=leading-equals",
		" SPACES_IN_KEY =value", // key trimmed to SPACES_IN_KEY — not on allowlist, should drop
	}, nil)
	if len(got) != 1 || got[0] != "PATH=/usr/bin" {
		t.Fatalf("tolerant parse wrong: %v", got)
	}
}

// Regression: Windows uses "Path" instead of "PATH". A case-sensitive
// allowlist used to drop it, leaving the spawned cmd.exe with no PATH
// and breaking node lookup for npm-shimmed CLIs.
func TestBuildAllowlistedSpawnEnvCaseInsensitive(t *testing.T) {
	t.Parallel()
	parent := []string{
		"Path=C:\\Program Files\\nodejs;C:\\Windows",
		"TeMp=C:\\Users\\a\\AppData\\Local\\Temp",
		"Bogus=should-drop",
	}
	got := buildAllowlistedSpawnEnv(parent, nil)
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "Path=C:\\Program Files\\nodejs;C:\\Windows") {
		t.Errorf("Windows-style Path should propagate: %v", got)
	}
	if !strings.Contains(text, "TeMp=C:\\Users\\a\\AppData\\Local\\Temp") {
		t.Errorf("mixed-case TEMP should propagate: %v", got)
	}
	if strings.Contains(text, "Bogus=") {
		t.Errorf("non-allowlisted key still dropped: %v", got)
	}
}

func TestBuildAllowlistedSpawnEnvIgnoresEmptyOverrideKey(t *testing.T) {
	t.Parallel()
	got := buildAllowlistedSpawnEnv(nil, map[string]string{"": "ignored", "  ": "also", "OK": "v"})
	text := strings.Join(got, "\n")
	if !strings.Contains(text, "OK=v") {
		t.Fatalf("valid override missing: %v", got)
	}
	for _, bad := range []string{"=ignored", " =also", "=also"} {
		if strings.Contains(text, bad) {
			t.Fatalf("empty-key override should be dropped (%q): %v", bad, got)
		}
	}
}
