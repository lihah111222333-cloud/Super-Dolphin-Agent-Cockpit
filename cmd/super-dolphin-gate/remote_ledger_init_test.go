package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRunRemoteLedgerInitCreatesCurrentSchema(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	if err := runRemote([]string{"init-ledger", "--ledger", ledgerPath}, strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("runRemote(init-ledger) error = %v", err)
	}
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := store.LoadRemoteBaselineState(); !errors.Is(err, gatecontract.ErrRemoteBaselineStateNotFound) {
		t.Fatalf("LoadRemoteBaselineState() error = %v, want empty initialized authority", err)
	}
	info, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatalf("stat initialized ledger: %v", err)
	}
	if info.Size() == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("initialized ledger size/mode = %d/%#o, want non-empty/0600", info.Size(), info.Mode().Perm())
	}
}

func TestRunRemoteLedgerInitInitializesZeroByteFile(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	if err := os.WriteFile(ledgerPath, nil, 0o600); err != nil {
		t.Fatalf("create zero-byte ledger: %v", err)
	}
	if err := runRemoteLedgerInit([]string{"--ledger", ledgerPath}); err != nil {
		t.Fatalf("runRemoteLedgerInit() error = %v", err)
	}
	info, err := os.Stat(ledgerPath)
	if err != nil {
		t.Fatalf("stat initialized ledger: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("zero-byte ledger was not initialized")
	}
}

func TestRunRemoteLedgerInitDoesNotOverwriteInvalidNonEmptyFile(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "remote-ci.baseline-state.sqlite")
	want := []byte("not-a-sqlite-authority")
	if err := os.WriteFile(ledgerPath, want, 0o600); err != nil {
		t.Fatalf("write invalid ledger fixture: %v", err)
	}
	if err := runRemoteLedgerInit([]string{"--ledger", ledgerPath}); err == nil {
		t.Fatal("runRemoteLedgerInit() error = nil, want invalid SQLite failure")
	}
	got, err := os.ReadFile(ledgerPath)
	if err != nil {
		t.Fatalf("read invalid ledger fixture: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("invalid non-empty ledger was overwritten: got %q want %q", got, want)
	}
}

func TestRunRemoteLedgerInitRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"--ledger", "relative.sqlite"},
		{"--ledger", filepath.Join(t.TempDir(), "ledger.sqlite"), "extra"},
	} {
		if err := runRemoteLedgerInit(args); err == nil {
			t.Fatalf("runRemoteLedgerInit(%q) error = nil, want failure", args)
		}
	}
}

func TestTrackedRemoteCIConfigPassesStrictValidation(t *testing.T) {
	configPath := filepath.Join("..", "..", "config", "remote-ci", "aliyun.json")
	config, err := loadRemoteRunConfig(configPath)
	if err != nil {
		t.Fatalf("load tracked remote CI config: %v", err)
	}
	if config.GenerationOneProvision == nil {
		t.Fatal("tracked remote CI config is missing generation-one provision receipt")
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read tracked remote CI config: %v", err)
	}
	invalid := bytes.Replace(raw, []byte(`"schema_version":10`), []byte(`"schema_version":9`), 1)
	if bytes.Equal(invalid, raw) {
		t.Fatal("tracked remote CI config schema marker was not found")
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid-remote-ci.json")
	if err := os.WriteFile(invalidPath, invalid, 0o600); err != nil {
		t.Fatalf("write invalid tracked config derivative: %v", err)
	}
	if _, err := loadRemoteRunConfig(invalidPath); err == nil {
		t.Fatal("tracked remote CI config guard accepted a drifted schema version")
	}
}
