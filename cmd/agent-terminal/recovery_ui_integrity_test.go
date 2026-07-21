package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestProbationDigestMismatchFlowsFromSelectorToWailsState(t *testing.T) {
	fixture := newAmbiguousRecoveryFixture(t)
	mustAgentTerminalNoError(t, os.Rename(filepath.Join(fixture.root, "external-old-release"), fixture.target))
	transaction := mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return fixture.store.Replay(t.Context(), fixture.txn.Identity)
	})
	transaction = mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return fixture.store.InstallCandidate(t.Context(), transaction.Identity)
	})
	process := recovery.ProcessIdentity{
		PID: 42, StartToken: "selector-integrity", ExecutableIdentity: "/candidate",
		ExecutableSHA256:    transaction.Identity.CandidateRelease.SHA256,
		TerminationEndpoint: filepath.Join(fixture.root, "candidate.sock"), TerminationToken: strings.Repeat("a", 64),
	}
	mustAgentTerminalValue(t, func() (recovery.ProbationLease, error) {
		return fixture.store.AcquireProbationLease(t.Context(), transaction.Identity, recovery.ProbationLeaseRequest{
			OwnerID: "selector-integrity", Process: process, TTL: time.Minute,
		})
	})
	mustAgentTerminalNoError(t, os.WriteFile(filepath.Join(transaction.Paths.Target, "tampered"), []byte("secret digest input"), 0o600))
	selection, selectErr := app.SelectStartup(t.Context(), app.StartupSelectorInput{
		Store: fixture.store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
		LeaseWait: time.Second, DigestTimeout: app.StartupDigestTimeout,
	})
	if !errors.Is(selectErr, app.ErrUpdateIntegrityInvalid) {
		t.Fatalf("SelectStartup() error = %v, want integrity failure", selectErr)
	}
	runtime := mustAgentTerminalValue(t, func() (*app.RecoveryRuntime, error) { return app.NewRecoveryRuntime(selection) })
	state, err := (&recoveryBinding{runtime: runtime}).State(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := app.RecoveryFailureForError(app.ErrUpdateIntegrityInvalid, transaction.Identity.TransactionID)
	if state.Failure != want {
		t.Fatalf("State().Failure = %#v, want %#v", state.Failure, want)
	}
	raw, err := json.Marshal(state.Failure)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 4 {
		t.Fatalf("failure fields = %#v, err=%v, want exactly four", fields, err)
	}
	if strings.Contains(state.Projection.Reason, fixture.root) || strings.Contains(state.Projection.Reason, "secret digest input") {
		t.Fatalf("safe recovery reason leaked digest input/path: %q", state.Projection.Reason)
	}
}
