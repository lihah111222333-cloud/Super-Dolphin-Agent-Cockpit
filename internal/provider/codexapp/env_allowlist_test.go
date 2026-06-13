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
		"SUPER_DOLPHIN_HOME=/Users/a/Library/Application Support/Super Dolphin",
		"SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=bootstrap-token",
		"SUPER_DOLPHIN_CODEX_RELAY_API_KEY=privileged-key",
		"CODEX_HOME=/stale/home",   // rogue — must be dropped
		"OPENAI_API_KEY=secret",    // rogue — must be dropped
		"AWS_SESSION_TOKEN=secret", // rogue — must be dropped
	}
	got := buildAllowlistedSpawnEnv(parent, nil)
	text := strings.Join(got, "\n")
	// Must keep.
	for _, want := range []string{"PATH=/usr/bin", "HOME=/home/a", "USER=a", "SUPER_DOLPHIN_HOME=/Users/a/Library/Application Support/Super Dolphin", "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN=bootstrap-token"} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q retained, got %v", want, got)
		}
	}
	// Must drop.
	for _, rogue := range []string{"CODEX_HOME=", "SUPER_DOLPHIN_CODEX_RELAY_API_KEY=", "OPENAI_API_KEY=", "AWS_SESSION_TOKEN="} {
		if strings.Contains(text, rogue) {
			t.Errorf("expected %q dropped, got %v", rogue, got)
		}
	}
}

func TestBuildAllowlistedSpawnEnvKeepsProxyEnvAndLoopbackNoProxy(t *testing.T) {
	t.Parallel()
	parent := []string{
		"PATH=/usr/bin",
		"HTTP_PROXY=http://127.0.0.1:7897",
		"https_proxy=http://127.0.0.1:7897",
		"ALL_PROXY=socks5://127.0.0.1:7890",
		"NO_PROXY=example.com,127.0.0.1",
	}
	got := buildAllowlistedSpawnEnv(parent, nil)
	text := strings.Join(got, "\n")
	for _, want := range []string{
		"HTTP_PROXY=http://127.0.0.1:7897",
		"https_proxy=http://127.0.0.1:7897",
		"ALL_PROXY=socks5://127.0.0.1:7890",
		"NO_PROXY=example.com,127.0.0.1,localhost,::1",
		"no_proxy=example.com,127.0.0.1,localhost,::1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("expected %q retained, got %v", want, got)
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
		"CODEX_HOME":                         "/canonicalized/home",
		"HOME":                               "/override/home", // override must win even for allowlisted keys
		"DATABASE_URL":                       "postgres://override@localhost/super_dolphin",
		"POSTGRES_CONNECTION_STRING":         "postgres://override-compat@localhost/super_dolphin",
		"SUPER_DOLPHIN_SQLITE_PATH":          "/private/override.db",
		"SUPER_DOLPHIN_INTERNAL_SQLITE_PATH": "/private/internal.db",
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
	requireCodexDatabaseEnvAbsent(t, got)
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
