package config

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLSPConfigIdleTimeoutDefaultsToFifteenMinutes(t *testing.T) {
	t.Setenv(lspIdleTimeoutEnv, "")
	t.Setenv(lspIdleTimeoutLegacyEnv, "")
	if err := os.Unsetenv(lspIdleTimeoutEnv); err != nil {
		t.Fatalf("unset canonical idle timeout: %v", err)
	}
	if err := os.Unsetenv(lspIdleTimeoutLegacyEnv); err != nil {
		t.Fatalf("unset legacy idle timeout: %v", err)
	}

	cfg, err := lspConfigFromEnv()
	if err != nil {
		t.Fatalf("lspConfigFromEnv() error = %v", err)
	}
	if cfg.IdleTimeout != 15*time.Minute {
		t.Fatalf("IdleTimeout = %s, want 15m", cfg.IdleTimeout)
	}
}

func TestLSPConfigIdleTimeoutEnvironmentMatrix(t *testing.T) {
	tests := []struct {
		name       string
		canonical  string
		legacy     string
		want       time.Duration
		wantErrSub string
	}{
		{name: "canonical override", canonical: "2500ms", want: 2500 * time.Millisecond},
		{name: "legacy alias", legacy: "3s", want: 3 * time.Second},
		{name: "matching aliases", canonical: "2s", legacy: "2000ms", want: 2 * time.Second},
		{name: "canonical legacy conflict", canonical: "2s", legacy: "3s", wantErrSub: "conflict"},
		{name: "canonical invalid", canonical: "not-a-duration", wantErrSub: lspIdleTimeoutEnv},
		{name: "legacy invalid", legacy: "not-a-duration", wantErrSub: lspIdleTimeoutLegacyEnv},
		{name: "canonical zero", canonical: "0", wantErrSub: lspIdleTimeoutEnv},
		{name: "legacy negative", legacy: "-1s", wantErrSub: lspIdleTimeoutLegacyEnv},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setOrUnsetLSPEnv(t, lspIdleTimeoutEnv, test.canonical)
			setOrUnsetLSPEnv(t, lspIdleTimeoutLegacyEnv, test.legacy)

			cfg, err := lspConfigFromEnv()
			if test.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrSub) {
					t.Fatalf("lspConfigFromEnv() error = %v, want substring %q", err, test.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("lspConfigFromEnv() error = %v", err)
			}
			if cfg.IdleTimeout != test.want {
				t.Fatalf("IdleTimeout = %s, want %s", cfg.IdleTimeout, test.want)
			}
		})
	}
}

func setOrUnsetLSPEnv(t *testing.T, key, value string) {
	t.Helper()
	t.Setenv(key, value)
	if value == "" {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}
}

func TestLSPConfigIdleTimeoutRejectsExplicitEmptyValues(t *testing.T) {
	for _, key := range []string{lspIdleTimeoutEnv, lspIdleTimeoutLegacyEnv} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(lspIdleTimeoutEnv, "")
			t.Setenv(lspIdleTimeoutLegacyEnv, "")
			if err := os.Unsetenv(lspIdleTimeoutEnv); err != nil {
				t.Fatalf("unset canonical idle timeout: %v", err)
			}
			if err := os.Unsetenv(lspIdleTimeoutLegacyEnv); err != nil {
				t.Fatalf("unset legacy idle timeout: %v", err)
			}
			t.Setenv(key, "")

			_, err := lspConfigFromEnv()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("lspConfigFromEnv() error = %v, want key %q", err, key)
			}
		})
	}
}

func TestLSPConfigIdleTimeoutLegacyAliasLogsDeprecation(t *testing.T) {
	setOrUnsetLSPEnv(t, lspIdleTimeoutEnv, "")
	t.Setenv(lspIdleTimeoutLegacyEnv, "2s")
	var logs bytes.Buffer
	restoreConfigLogger(t, &logs)

	if _, err := lspConfigFromEnv(); err != nil {
		t.Fatalf("lspConfigFromEnv() error = %v", err)
	}
	for _, want := range []string{"config env deprecated", lspIdleTimeoutLegacyEnv, lspIdleTimeoutEnv} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("legacy warning = %q, want substring %q", logs.String(), want)
		}
	}
}

func TestLSPConfigClonePreservesIdleTimeout(t *testing.T) {
	cfg := cloneLSPConfig(DefaultLSPConfig())
	cfg.IdleTimeout = 7 * time.Minute
	clone := cloneLSPConfig(cfg)
	if clone.IdleTimeout != cfg.IdleTimeout {
		t.Fatalf("clone IdleTimeout = %s, want %s", clone.IdleTimeout, cfg.IdleTimeout)
	}
	if !slices.Equal(clone.NoiseDirNames, cfg.NoiseDirNames) {
		t.Fatalf("clone changed unrelated LSP fields")
	}
}
