package acpnode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testLaunchConfig(t *testing.T) LaunchConfig {
	t.Helper()
	return LaunchConfig{
		Enabled:         true,
		Executable:      os.Args[0],
		CWD:             t.TempDir(),
		Env:             []string{"PATH=/usr/bin"},
		EnvAllowlist:    []string{"PATH"},
		StartupTimeout:  time.Second,
		RequestTimeout:  time.Second,
		ShutdownTimeout: time.Second,
		MaxMessage:      DefaultMaxMessage,
		MaxStderr:       DefaultMaxStderr,
	}
}

func TestLaunchConfigRejectsUnsafeBoundary(t *testing.T) {
	base := testLaunchConfig(t)
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*LaunchConfig){
		"cwd file":      func(c *LaunchConfig) { c.CWD = file },
		"duplicate env": func(c *LaunchConfig) { c.Env = []string{"PATH=/usr/bin", "PATH=/bin"} },
		"unknown env":   func(c *LaunchConfig) { c.Env = []string{"SECRET=hidden"} },
		"nul argv":      func(c *LaunchConfig) { c.Args = []string{"bad\x00arg"} },
		"zero startup":  func(c *LaunchConfig) { c.StartupTimeout = 0 },
		"zero message":  func(c *LaunchConfig) { c.MaxMessage = 0 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("Validate() accepted unsafe config")
			}
		})
	}
}

func TestLaunchConfigAcceptsExplicitCustomEnvAllowlist(t *testing.T) {
	cfg := testLaunchConfig(t)
	cfg.Env = []string{"FAKE_PEER_MODE=1"}
	cfg.EnvAllowlist = []string{"FAKE_PEER_MODE"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLaunchConfigRejectsMissingExplicitEnvironmentOrAllowlist(t *testing.T) {
	base := testLaunchConfig(t)
	for name, mutate := range map[string]func(*LaunchConfig){
		"missing env":       func(c *LaunchConfig) { c.Env = nil },
		"missing allowlist": func(c *LaunchConfig) { c.EnvAllowlist = nil },
		"empty allowlist":   func(c *LaunchConfig) { c.EnvAllowlist = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate() accepted implicit environment configuration")
			}
		})
	}
}

func TestLaunchConfigRejectsDuplicateAllowlistAndMalformedKeys(t *testing.T) {
	cfg := testLaunchConfig(t)
	cfg.EnvAllowlist = []string{"PATH", "PATH"}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("duplicate allowlist error = %v", err)
	}
	cfg.EnvAllowlist = []string{"PATH=BAD"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("malformed allowlist accepted")
	}
}

func TestConfigProvenanceIsOfflineAndDigestBound(t *testing.T) {
	if err := validateConfigProvenanceBytes(nil); err == nil {
		t.Fatal("missing provenance receipt accepted")
	}
	if err := validateConfigProvenance(); err != nil {
		t.Fatal(err)
	}
	var receipt configProvenanceReceipt
	if err := json.Unmarshal([]byte(embeddedConfigProvenance), &receipt); err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(receipt.CanonicalConfigBase64)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1232 {
		t.Fatalf("canonical config bytes = %d, want 1232", len(decoded))
	}
	mutated := append([]byte(nil), []byte(embeddedConfigProvenance)...)
	mutated[len(mutated)-2] ^= 1
	if err := validateConfigProvenanceBytes(mutated); err == nil {
		t.Fatal("mutated provenance receipt accepted")
	}
	digestMutated := bytes.Replace([]byte(embeddedConfigProvenance), []byte(configProvenanceConfigSHA256), []byte(strings.Repeat("0", len(configProvenanceConfigSHA256))), 1)
	if err := validateConfigProvenanceBytes(digestMutated); err == nil {
		t.Fatal("mismatched provenance digest accepted")
	}
}
