package main

import (
	"strings"
	"testing"
)

func TestParseRemoteCleanupOptionsRequiresOnlyExplicitCleanupAuthority(t *testing.T) {
	valid, err := parseRemoteCleanupOptions([]string{"--job-id", "job-cleanup", "--config", "/tmp/remote.json", "--ledger", "/tmp/ledger.sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if valid.jobID != "job-cleanup" || valid.configPath != "/tmp/remote.json" || valid.ledgerPath != "/tmp/ledger.sqlite" {
		t.Fatalf("cleanup options = %#v", valid)
	}
	for _, args := range [][]string{
		nil,
		{"--job-id", "job-cleanup", "--config", "/tmp/remote.json"},
		{"--job-id", "job-cleanup", "--config", "/tmp/remote.json", "--ledger", "/tmp/ledger.sqlite", "unexpected"},
		{"--job-id", "job-cleanup", "--config", "/tmp/remote.json", "--ledger", "/tmp/ledger.sqlite", "--scenario", "push"},
	} {
		if _, err := parseRemoteCleanupOptions(args); err == nil || !strings.Contains(err.Error(), "remote cleanup requires") {
			t.Fatalf("parseRemoteCleanupOptions(%q) error = %v", args, err)
		}
	}
	if _, err := parseRemoteCleanupOptions([]string{"--job-id", "job-one", "--job-id=job-two", "--config", "/tmp/remote.json", "--ledger", "/tmp/ledger.sqlite"}); err == nil || !strings.Contains(err.Error(), "duplicate authority flag --job-id") {
		t.Fatalf("duplicate cleanup job ID error = %v", err)
	}
}

func TestRunRemoteRoutesCleanupWithoutAgentTokenProtocol(t *testing.T) {
	err := runRemote([]string{"cleanup"}, strings.NewReader(""), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "remote cleanup requires") {
		t.Fatalf("runRemote(cleanup) error = %v", err)
	}
}
