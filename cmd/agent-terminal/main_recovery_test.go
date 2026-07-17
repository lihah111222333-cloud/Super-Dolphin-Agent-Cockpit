package main

import (
	"context"
	"errors"
	"io"
	"slices"
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

func TestRouteFilesystemHelperModesChecksReleaseBeforeSchema(t *testing.T) {
	events := make([]string, 0, 2)
	modes := []filesystemHelperMode{
		{name: "release", run: func(io.Reader, io.Writer) (bool, error) {
			events = append(events, "release")
			return false, nil
		}},
		{name: "schema", run: func(io.Reader, io.Writer) (bool, error) {
			events = append(events, "schema")
			return true, nil
		}},
	}
	handled, name, err := routeFilesystemHelperModes(nil, nil, modes)
	if !handled || name != "schema" || err != nil {
		t.Fatalf("route result = handled %t name %q error %v", handled, name, err)
	}
	if want := []string{"release", "schema"}; !slices.Equal(events, want) {
		t.Fatalf("filesystem helper route events = %v, want %v", events, want)
	}
}

func TestRouteFilesystemHelperModesStopsAfterReleaseHandles(t *testing.T) {
	modes := []filesystemHelperMode{
		{name: "release", run: func(io.Reader, io.Writer) (bool, error) { return true, nil }},
		{name: "schema", run: func(io.Reader, io.Writer) (bool, error) {
			t.Fatal("schema helper recursively routed after release helper handled")
			return false, nil
		}},
	}
	handled, name, err := routeFilesystemHelperModes(nil, nil, modes)
	if !handled || name != "release" || err != nil {
		t.Fatalf("route result = handled %t name %q error %v", handled, name, err)
	}
}

type fakeTerminationServer struct {
	closed    bool
	activated bool
	waitErr   error
	closeErr  error
}

func prepareTestFilesystemHelper() (func() error, error) {
	return func() error { return nil }, nil
}

func (server *fakeTerminationServer) WaitForActivation(context.Context) error {
	server.activated = true
	return server.waitErr
}

func (server *fakeTerminationServer) Close() error {
	server.closed = true
	return server.closeErr
}

func TestRunMainErrorExitRunsTerminationCleanup(t *testing.T) {
	server := &fakeTerminationServer{}
	stopped := false
	runErr := errors.New("desktop failed")
	exitCode := runMain(t.Context(), func() { stopped = true }, terminalMainDeps{
		prepareReleaseFilesystemHelper: prepareTestFilesystemHelper,
		prepareSchemaFilesystemWorker:  prepareTestFilesystemHelper,
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			return server, false, nil
		},
		run: func(context.Context, terminalDeps) error { return runErr },
	})
	if exitCode != 1 || !server.closed || !stopped {
		t.Fatalf("runMain exit=%d closed=%t stopped=%t, want 1/true/true", exitCode, server.closed, stopped)
	}
}

func TestRunMainParkedWaitFailureRunsCleanup(t *testing.T) {
	waitErr := errors.New("activation failed")
	server := &fakeTerminationServer{waitErr: waitErr}
	exitCode := runMain(t.Context(), func() {}, terminalMainDeps{
		prepareReleaseFilesystemHelper: prepareTestFilesystemHelper,
		prepareSchemaFilesystemWorker:  prepareTestFilesystemHelper,
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			return server, true, nil
		},
		run: func(context.Context, terminalDeps) error {
			t.Fatal("desktop ran after activation failure")
			return nil
		},
	})
	if exitCode != 1 || !server.activated || !server.closed {
		t.Fatalf("runMain exit=%d activated=%t closed=%t, want 1/true/true", exitCode, server.activated, server.closed)
	}
}

func TestRunMainPreparesBothHelpersAndCleansInReverseOrder(t *testing.T) {
	events := make([]string, 0, 7)
	exitCode := runMain(t.Context(), func() { events = append(events, "stop") }, terminalMainDeps{
		prepareReleaseFilesystemHelper: recordedHelperPreparation(&events, "prepare-release", "cleanup-release", nil),
		prepareSchemaFilesystemWorker:  recordedHelperPreparation(&events, "prepare-schema", "cleanup-schema", nil),
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			events = append(events, "termination")
			return nil, false, nil
		},
		run: func(context.Context, terminalDeps) error {
			events = append(events, "run")
			return nil
		},
	})
	if exitCode != 0 {
		t.Fatalf("runMain exit = %d, want 0", exitCode)
	}
	want := []string{"prepare-release", "prepare-schema", "termination", "run", "cleanup-schema", "cleanup-release", "stop"}
	if !slices.Equal(events, want) {
		t.Fatalf("runMain events = %v, want %v", events, want)
	}
}

func TestRunMainSchemaPreparationFailureCleansReleaseAndStopsStartup(t *testing.T) {
	events := make([]string, 0, 4)
	prepareErr := errors.New("schema preparation failed")
	exitCode := runMain(t.Context(), func() { events = append(events, "stop") }, terminalMainDeps{
		prepareReleaseFilesystemHelper: recordedHelperPreparation(&events, "prepare-release", "cleanup-release", nil),
		prepareSchemaFilesystemWorker: func() (func() error, error) {
			events = append(events, "prepare-schema")
			return nil, prepareErr
		},
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			t.Fatal("termination server started after schema preparation failure")
			return nil, false, nil
		},
		run: func(context.Context, terminalDeps) error {
			t.Fatal("desktop ran after schema preparation failure")
			return nil
		},
	})
	if exitCode != 1 {
		t.Fatalf("runMain exit = %d, want 1", exitCode)
	}
	want := []string{"prepare-release", "prepare-schema", "cleanup-release", "stop"}
	if !slices.Equal(events, want) {
		t.Fatalf("runMain events = %v, want %v", events, want)
	}
}

func TestRunMainHelperCleanupErrorChangesSuccessfulExit(t *testing.T) {
	cleanupErr := errors.New("schema cleanup failed")
	exitCode := runMain(t.Context(), func() {}, terminalMainDeps{
		prepareReleaseFilesystemHelper: prepareTestFilesystemHelper,
		prepareSchemaFilesystemWorker: func() (func() error, error) {
			return func() error { return cleanupErr }, nil
		},
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			return nil, false, nil
		},
		run: func(context.Context, terminalDeps) error { return nil },
	})
	if exitCode != 1 {
		t.Fatalf("runMain cleanup error exit = %d, want 1", exitCode)
	}
}

func recordedHelperPreparation(events *[]string, prepare, cleanup string, cleanupErr error) func() (func() error, error) {
	return func() (func() error, error) {
		*events = append(*events, prepare)
		return func() error {
			*events = append(*events, cleanup)
			return cleanupErr
		}, nil
	}
}
