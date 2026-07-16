package main

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
)

func TestAgentTerminalSelectsRecoveryBeforeNormalPreflight(t *testing.T) {
	normalCalls := 0
	recoveryCalls := 0
	err := runAgentTerminal(context.Background(), terminalDeps{
		selectStartup: func(context.Context) (app.StartupSelection, error) {
			return app.StartupSelection{Mode: app.StartupModeRecovery}, nil
		},
		runNormal: func(context.Context, app.StartupSelection) error {
			normalCalls++
			return nil
		},
		runRecovery: func(context.Context, app.StartupSelection) error {
			recoveryCalls++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runAgentTerminal() error = %v", err)
	}
	if normalCalls != 0 || recoveryCalls != 1 {
		t.Fatalf("calls = normal %d recovery %d, want 0/1", normalCalls, recoveryCalls)
	}
}

func TestFirstNormalPreflightFailureOpensRecoverySurface(t *testing.T) {
	preflightErr := errors.New("first normal preflight failed")
	recoveryCalls := 0
	err := runAgentTerminal(context.Background(), terminalDeps{
		selectStartup: func(context.Context) (app.StartupSelection, error) {
			return app.StartupSelection{Mode: app.StartupModeNormal}, nil
		},
		runNormal: func(context.Context, app.StartupSelection) error { return preflightErr },
		runRecovery: func(_ context.Context, selection app.StartupSelection) error {
			recoveryCalls++
			if selection.Mode != app.StartupModeRecovery || selection.Projection.Reason != preflightErr.Error() {
				t.Fatalf("Recovery selection = %#v", selection)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runAgentTerminal() error = %v", err)
	}
	if recoveryCalls != 1 {
		t.Fatalf("Recovery calls = %d, want 1", recoveryCalls)
	}
}

func TestActiveProbationNormalFailureDoesNotOpenRecovery(t *testing.T) {
	startErr := errors.New("candidate startup failed")
	recoveryCalls := 0
	selection := app.StartupSelection{
		Mode: app.StartupModeNormal,
		Transaction: recovery.Transaction{
			State:    recovery.StateProbation,
			Identity: recovery.Identity{TransactionID: recovery.TransactionID("active-probation")},
			Probation: recovery.ProbationRecord{
				LeasePresent: true,
				Lease: recovery.ProbationLease{
					OwnerID: "updater", Generation: 1,
					Process: recovery.ProcessIdentity{
						PID: 42, StartToken: "start", ExecutableIdentity: "/candidate", ExecutableSHA256: "digest",
					},
				},
			},
		},
	}
	err := runAgentTerminal(context.Background(), terminalDeps{
		selectStartup: func(context.Context) (app.StartupSelection, error) { return selection, nil },
		runNormal:     func(context.Context, app.StartupSelection) error { return startErr },
		runRecovery: func(context.Context, app.StartupSelection) error {
			recoveryCalls++
			return nil
		},
	})
	if !errors.Is(err, startErr) {
		t.Fatalf("runAgentTerminal() error = %v, want %v", err, startErr)
	}
	if recoveryCalls != 0 {
		t.Fatalf("Recovery calls = %d, want 0", recoveryCalls)
	}
}
