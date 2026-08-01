package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRequesterCreateWritesOneCanonicalFingerprintLine(t *testing.T) {
	t.Parallel()

	code, stdout, stderr := executeCLI([]string{"requester", "create"})
	if code != int(gatecontract.ExitOK) || stderr != "" {
		t.Fatalf("requester create code=%d stderr=%q", code, stderr)
	}
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("requester create stdout = %q, want exactly one line", stdout)
	}
	value := strings.TrimSuffix(stdout, "\n")
	if _, err := gatecontract.ParseRequesterFingerprint(value); err != nil {
		t.Fatalf("requester create output %q is invalid: %v", value, err)
	}
}

func TestRequesterCLIRejectsMissingUnknownAndAdditionalArguments(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		args    []string
		message string
	}{
		{args: []string{"requester"}, message: "requester subcommand is required"},
		{args: []string{"requester", "inspect"}, message: "requester subcommand must be"},
		{args: []string{"requester", "create", "extra"}, message: "requester create accepts no arguments"},
	} {
		code, stdout, stderr := executeCLI(test.args)
		if code != int(gatecontract.ExitProtocol) || stdout != "" ||
			!strings.Contains(stderr, test.message) {
			t.Fatalf("args=%v code=%d stdout=%q stderr=%q", test.args, code, stdout, stderr)
		}
	}
}

func TestRequesterCreateClassifiesStdoutFailure(t *testing.T) {
	t.Parallel()

	stderr := &bytes.Buffer{}
	code := runCLI(
		[]string{"requester", "create"},
		failingWriter{err: errors.New("write failed")},
		stderr,
	)
	if code != int(gatecontract.ExitInfrastructure) ||
		!strings.Contains(stderr.String(), "write requester fingerprint") {
		t.Fatalf("requester create writer failure code=%d stderr=%q", code, stderr.String())
	}
}

func TestResolveRequesterFingerprintRejectsAmbiguousSources(t *testing.T) {
	t.Parallel()

	first := "sha256:" + strings.Repeat("1", 64)
	second := "sha256:" + strings.Repeat("2", 64)
	if _, err := resolveRequesterFingerprint(first, second); err == nil ||
		!strings.Contains(err.Error(), "conflict") {
		t.Fatalf("resolve requester conflict error = %v", err)
	}
	fingerprint, err := resolveRequesterFingerprint("", first)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint.String() != first {
		t.Fatalf("resolved requester = %q, want %q", fingerprint, first)
	}
}

func TestRequesterRunsUsesVendorNeutralEnvironment(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	store, err := gatecontract.NewDurationLedgerStore(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, gatecontract.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	fingerprint := "sha256:" + strings.Repeat("3", 64)
	t.Setenv(gatecontract.RequesterFingerprintEnvironment, fingerprint)
	code, stdout, stderr := executeCLI([]string{
		"requester", "runs",
		"--ledger", ledgerPath,
	})
	if code != int(gatecontract.ExitOK) || stderr != "" {
		t.Fatalf("requester runs code=%d stderr=%q", code, stderr)
	}
	var result requesterRunsResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode requester runs: %v; output=%q", err, stdout)
	}
	if result.RequesterFingerprint.String() != fingerprint || len(result.JobIDs) != 0 {
		t.Fatalf("requester runs result = %#v", result)
	}
}
