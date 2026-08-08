package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/app"
	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const agentTerminalFilesystemHelperExecutableEnv = "SUPER_DOLPHIN_RELEASE_FS_HELPER_EXECUTABLE"
const agentTerminalRollbackArtifactExecutableEnv = "SUPER_DOLPHIN_TEST_ROLLBACK_ARTIFACT"
const agentTerminalReleaseFilesystemHelperModeEnv = "SUPER_DOLPHIN_RELEASE_FS_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(agentTerminalReleaseFilesystemHelperModeEnv) == "1" {
		os.Exit(runAgentTerminalProcess())
	}
	os.Exit(runAgentTerminalTests(m))
}

func runAgentTerminalTests(m *testing.M) int {
	productionCleanup, err := prepareAgentTerminalProductionTestHelper()
	if err != nil {
		fmt.Fprintf(os.Stderr, "prepare production agent-terminal helper: %v\n", err)
		return 1
	}
	rollbackCleanup, err := prepareAgentTerminalRollbackArtifact()
	if err != nil {
		err = errors.Join(err, productionCleanup())
		fmt.Fprintf(os.Stderr, "prepare rollback agent-terminal helper: %v\n", err)
		return 1
	}
	exitCode := m.Run()
	if err := errors.Join(rollbackCleanup(), productionCleanup()); err != nil {
		fmt.Fprintf(os.Stderr, "cleanup production agent-terminal helper: %v\n", err)
		if exitCode == 0 {
			return 1
		}
	}
	return exitCode
}

func prepareAgentTerminalRollbackArtifact() (func() error, error) {
	dir, err := os.MkdirTemp("", "agent-terminal-rollback-artifact-")
	if err != nil {
		return nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(agentTerminalRollbackArtifactSource), 0o600); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	artifact := filepath.Join(dir, "rollback-artifact")
	output, err := exec.Command("go", "build", "-trimpath", "-o", artifact, source).CombinedOutput()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("build rollback artifact: %w: %s", err, output), cleanup())
	}
	previous, hadPrevious := os.LookupEnv(agentTerminalRollbackArtifactExecutableEnv)
	if err := os.Setenv(agentTerminalRollbackArtifactExecutableEnv, artifact); err != nil {
		return nil, errors.Join(err, cleanup())
	}
	return func() error {
		return errors.Join(
			restoreAgentTerminalTestEnv(agentTerminalRollbackArtifactExecutableEnv, previous, hadPrevious),
			cleanup(),
		)
	}, nil
}

func TestUseHeadlessDesktopBackendOnlyForRemoteWorker(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		backend string
		want    bool
	}{
		{name: "remote worker", backend: "remote-worker", want: true},
		{name: "unsupported backend", backend: "unsupported", want: false},
		{name: "desktop", backend: "", want: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := useHeadlessDesktopBackend(testCase.backend); got != testCase.want {
				t.Fatalf("useHeadlessDesktopBackend(%q) = %t, want %t", testCase.backend, got, testCase.want)
			}
		})
	}
}

func agentTerminalRollbackHoldPath(launch runtimeenv.RecoveryLaunch) string {
	return filepath.Join(filepath.Dir(launch.TransactionRoot), ".agent-terminal-rollback-hold-"+launch.TransactionID)
}

func agentTerminalRollbackLaunchDir(launch runtimeenv.RecoveryLaunch) string {
	return filepath.Join(filepath.Dir(launch.TransactionRoot), ".agent-terminal-rollback-launches-"+launch.TransactionID)
}

func buildAgentTerminalRollbackArtifact(t *testing.T) string {
	t.Helper()
	cachedArtifact := os.Getenv(agentTerminalRollbackArtifactExecutableEnv)
	if cachedArtifact == "" {
		t.Fatal("rollback artifact helper environment is required")
	}
	raw, err := os.ReadFile(cachedArtifact)
	if err != nil {
		t.Fatalf("read cached rollback artifact: %v", err)
	}
	artifact := filepath.Join(t.TempDir(), "rollback-artifact")
	if err := os.WriteFile(artifact, raw, 0o700); err != nil {
		t.Fatalf("copy cached rollback artifact: %v", err)
	}
	return artifact
}

const agentTerminalRollbackArtifactSource = `package main

import (
	"bufio"
	"crypto/subtle"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const launchTokenPrefix = "--super-dolphin-rollback-launch-token="

func main() {
	token, ok := rollbackLaunchToken(os.Args[1:])
	endpoint := os.Getenv("SUPER_DOLPHIN_UPDATE_TERMINATION_ENDPOINT")
	controlToken := os.Getenv("SUPER_DOLPHIN_UPDATE_TERMINATION_TOKEN")
	transactionRoot := os.Getenv("SUPER_DOLPHIN_UPDATE_TRANSACTION_ROOT")
	transactionID := os.Getenv("SUPER_DOLPHIN_UPDATE_TRANSACTION_ID")
	if !ok || endpoint == "" || controlToken == "" || transactionRoot == "" || transactionID == "" {
		os.Exit(30)
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(controlToken)) != 1 || !filepath.IsAbs(transactionRoot) {
		os.Exit(31)
	}
	oldUmask := syscall.Umask(0177)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	syscall.Umask(oldUmask)
	if err != nil {
		os.Exit(32)
	}
	defer listener.Close()
	defer os.Remove(endpoint)
	if err := os.Chmod(endpoint, 0600); err != nil {
		os.Exit(33)
	}
	if err := recordLaunch(transactionRoot, transactionID); err != nil {
		os.Exit(34)
	}
	serve(listener, controlToken, transactionRoot, transactionID)
}

func rollbackLaunchToken(args []string) (string, bool) {
	for _, argument := range args {
		if token, found := strings.CutPrefix(argument, launchTokenPrefix); found {
			return token, token != ""
		}
	}
	return "", false
}

func recordLaunch(transactionRoot, transactionID string) error {
	dir := filepath.Join(filepath.Dir(transactionRoot), ".agent-terminal-rollback-launches-"+transactionID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0600)
}

func serve(listener *net.UnixListener, token, transactionRoot, transactionID string) {
	hold := filepath.Join(filepath.Dir(transactionRoot), ".agent-terminal-rollback-hold-"+transactionID)
	readyCount := 0
	for {
		if _, err := os.Stat(hold); os.IsNotExist(err) {
			return
		} else if err != nil {
			os.Exit(35)
		}
		if err := listener.SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			os.Exit(36)
		}
		connection, err := listener.AcceptUnix()
		if networkError, ok := err.(net.Error); ok && networkError.Timeout() {
			continue
		}
		if err != nil {
			os.Exit(37)
		}
		terminate := handle(connection, token, &readyCount)
		_ = connection.Close()
		if terminate {
			return
		}
	}
}

func handle(connection *net.UnixConn, token string, readyCount *int) bool {
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return false
	}
	line, err := bufio.NewReader(connection).ReadString('\n')
	command, gotToken, found := strings.Cut(strings.TrimSuffix(line, "\n"), " ")
	if err != nil || !found || subtle.ConstantTimeCompare([]byte(gotToken), []byte(token)) != 1 {
		return false
	}
	switch command {
	case "READY":
		(*readyCount)++
		_, _ = connection.Write([]byte("READY\n"))
	case "COMMIT":
		if *readyCount < 2 {
			_, _ = connection.Write([]byte("NOT_PREPARED\n"))
			return false
		}
		_, _ = connection.Write([]byte("COMMITTED\n"))
	case "TERMINATE":
		_, _ = connection.Write([]byte("ACK\n"))
		return true
	}
	return false
}
`

func TestAgentTerminalSelectsRecoveryBeforeNormalPreflight(t *testing.T) {
	normalCalls := 0
	recoveryCalls := 0
	err := runAgentTerminal(context.Background(), terminalDeps{
		selectStartup: func(context.Context) (app.StartupSelection, error) {
			return app.StartupSelection{Mode: app.StartupModeRecovery}, nil
		},
		prepareSchemaFilesystemWorker: func() (func() error, error) {
			t.Fatal("schema helper prepared in Recovery mode")
			return nil, nil
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
	preflightErr := fmt.Errorf("first normal preflight failed: %w", app.ErrUpdateSignatureInvalid)
	recoveryCalls := 0
	err := runAgentTerminal(context.Background(), terminalDeps{
		selectStartup: func(context.Context) (app.StartupSelection, error) {
			return app.StartupSelection{Mode: app.StartupModeNormal}, nil
		},
		prepareSchemaFilesystemWorker: prepareTestFilesystemHelper,
		runNormal:                     func(context.Context, app.StartupSelection) error { return preflightErr },
		runRecovery: func(_ context.Context, selection app.StartupSelection) error {
			recoveryCalls++
			if selection.Mode != app.StartupModeRecovery || selection.Projection.Reason != "Update signature verification failed; recovery state was preserved." {
				t.Fatalf("Recovery selection = %#v", selection)
			}
			if selection.Failure.Code != "UPDATE_SIGNATURE_INVALID" ||
				selection.Failure.Action != app.RecoveryActionPreserveStateExportDiagnostics {
				t.Fatalf("Recovery failure = %#v", selection.Failure)
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
		selectStartup:                 func(context.Context) (app.StartupSelection, error) { return selection, nil },
		prepareSchemaFilesystemWorker: prepareTestFilesystemHelper,
		runNormal:                     func(context.Context, app.StartupSelection) error { return startErr },
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
		prepareSchemaFilesystemWorker: func() (func() error, error) {
			t.Fatal("schema helper prepared before parked activation")
			return nil, nil
		},
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

func TestRunMainStartsTerminationBeforeSchemaPreparation(t *testing.T) {
	events := make([]string, 0, 8)
	exitCode := runMain(t.Context(), func() { events = append(events, "stop") }, terminalMainDeps{
		prepareReleaseFilesystemHelper: recordedHelperPreparation(&events, "prepare-release", "cleanup-release", nil),
		prepareSchemaFilesystemWorker:  recordedHelperPreparation(&events, "prepare-schema", "cleanup-schema", nil),
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			events = append(events, "termination")
			return nil, false, nil
		},
		run: runAgentTerminal,
		terminal: terminalDeps{
			selectStartup: func(context.Context) (app.StartupSelection, error) {
				events = append(events, "select")
				return app.StartupSelection{Mode: app.StartupModeNormal}, nil
			},
			runNormal: func(context.Context, app.StartupSelection) error {
				events = append(events, "run")
				return nil
			},
			runRecovery: func(context.Context, app.StartupSelection) error {
				t.Fatal("Recovery ran during normal startup")
				return nil
			},
		},
	})
	if exitCode != 0 {
		t.Fatalf("runMain exit = %d, want 0", exitCode)
	}
	want := []string{"prepare-release", "termination", "select", "prepare-schema", "run", "cleanup-schema", "cleanup-release", "stop"}
	if !slices.Equal(events, want) {
		t.Fatalf("runMain events = %v, want %v", events, want)
	}
}

func TestRunMainSchemaPreparationFailureKeepsTerminationReady(t *testing.T) {
	events := make([]string, 0, 5)
	prepareErr := errors.New("schema preparation failed")
	server := &fakeTerminationServer{}
	exitCode := runMain(t.Context(), func() { events = append(events, "stop") }, terminalMainDeps{
		prepareReleaseFilesystemHelper: recordedHelperPreparation(&events, "prepare-release", "cleanup-release", nil),
		prepareSchemaFilesystemWorker: func() (func() error, error) {
			events = append(events, "prepare-schema")
			return nil, prepareErr
		},
		startTermination: func(context.CancelFunc) (cooperativeTerminationServer, bool, error) {
			events = append(events, "termination")
			return server, false, nil
		},
		run: runAgentTerminal,
		terminal: terminalDeps{
			selectStartup: func(context.Context) (app.StartupSelection, error) {
				return app.StartupSelection{Mode: app.StartupModeNormal}, nil
			},
			runNormal: func(context.Context, app.StartupSelection) error {
				t.Fatal("desktop ran after schema preparation failure")
				return nil
			},
			runRecovery: func(context.Context, app.StartupSelection) error {
				t.Fatal("Recovery ran after schema preparation failure")
				return nil
			},
		},
	})
	if exitCode != 1 {
		t.Fatalf("runMain exit = %d, want 1", exitCode)
	}
	if !server.closed {
		t.Fatal("termination server was not closed after schema preparation failure")
	}
	want := []string{"prepare-release", "termination", "prepare-schema", "cleanup-release", "stop"}
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
		run: runAgentTerminal,
		terminal: terminalDeps{
			selectStartup: func(context.Context) (app.StartupSelection, error) {
				return app.StartupSelection{Mode: app.StartupModeNormal}, nil
			},
			runNormal:   func(context.Context, app.StartupSelection) error { return nil },
			runRecovery: func(context.Context, app.StartupSelection) error { return nil },
		},
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

func TestRecoveryRestoreProductionCallbacksUseCurrentHelperEnvironment(t *testing.T) {
	store, rolledBack := createAgentTerminalProductionRecovery(t)
	staleSelection := rolledBack
	staleSelection.Paths.Target = filepath.Join(t.TempDir(), "stale-selection.app")
	runtime := newAgentTerminalRecoveryRuntime(t, store, staleSelection)
	launch := agentTerminalTestRecoveryLaunch(rolledBack)
	mustAgentTerminalNoError(t, os.WriteFile(agentTerminalRollbackHoldPath(launch), nil, 0o600))
	t.Cleanup(func() { _ = os.Remove(agentTerminalRollbackHoldPath(launch)) })
	restored, repeated := restoreAgentTerminalProductionTwice(t, runtime)
	assertAgentTerminalProductionRestore(t, launch, rolledBack, restored, repeated)
	mustAgentTerminalNoError(t, os.Remove(agentTerminalRollbackHoldPath(launch)))
}

func newAgentTerminalRecoveryRuntime(
	t *testing.T,
	store *recovery.Store,
	transaction recovery.Transaction,
) *app.RecoveryRuntime {
	t.Helper()
	return mustAgentTerminalValue(t, func() (*app.RecoveryRuntime, error) {
		return app.NewRecoveryRuntime(app.StartupSelection{
			Mode: app.StartupModeRecovery, Store: store, Transaction: transaction,
			Projection: app.RecoveryProjection{
				TransactionID: transaction.Identity.TransactionID, AttemptID: transaction.Identity.AttemptID,
				State: recovery.StateRolledBack,
			},
		})
	})
}

func agentTerminalTestRecoveryLaunch(transaction recovery.Transaction) runtimeenv.RecoveryLaunch {
	return runtimeenv.RecoveryLaunch{
		TransactionRoot: recovery.TransactionRootForTarget(transaction.Paths.Target),
		TransactionID:   string(transaction.Identity.TransactionID),
	}
}

func restoreAgentTerminalProductionTwice(
	t *testing.T,
	runtime *app.RecoveryRuntime,
) (recovery.Transaction, recovery.Transaction) {
	t.Helper()
	restored := mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return runtime.Restore.Restore(t.Context())
	})
	repeated := mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return runtime.Restore.Restore(t.Context())
	})
	return restored, repeated
}

func assertAgentTerminalProductionRestore(
	t *testing.T,
	launch runtimeenv.RecoveryLaunch,
	rolledBack, restored, repeated recovery.Transaction,
) {
	t.Helper()
	if !restored.RollbackRestart.ACKPresent || !repeated.RollbackRestart.ACKPresent {
		t.Fatalf("rollback restart ACKs = %#v / %#v", restored.RollbackRestart, repeated.RollbackRestart)
	}
	if restored.RollbackRestart.ACK != repeated.RollbackRestart.ACK {
		t.Fatalf("repeated ACK changed: %#v / %#v", restored.RollbackRestart.ACK, repeated.RollbackRestart.ACK)
	}
	if restored.RollbackRestart.ACK.LaunchToken != rolledBack.RollbackRestart.LaunchToken {
		t.Fatalf("ACK token = %q, want %q", restored.RollbackRestart.ACK.LaunchToken, rolledBack.RollbackRestart.LaunchToken)
	}
	launches, err := os.ReadDir(agentTerminalRollbackLaunchDir(launch))
	if err != nil {
		t.Fatal(err)
	}
	if len(launches) != 1 || launches[0].Name() != strconv.Itoa(restored.RollbackRestart.ACK.Process.PID) {
		t.Fatalf("production launches = %v, ACK PID = %d", launches, restored.RollbackRestart.ACK.Process.PID)
	}
}

func TestRecoveryRestoreProductionCallbackFailureDoesNotQuit(t *testing.T) {
	store, rolledBack := createAgentTerminalProductionRecovery(t)
	runtime, err := app.NewRecoveryRuntime(app.StartupSelection{
		Mode: app.StartupModeRecovery, Store: store, Transaction: rolledBack,
		Projection: app.RecoveryProjection{
			TransactionID: rolledBack.Identity.TransactionID, AttemptID: rolledBack.Identity.AttemptID,
			State: recovery.StateRolledBack,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(agentTerminalFilesystemHelperExecutableEnv, filepath.Join(t.TempDir(), "missing-helper"))
	quitCalls := 0
	_, err = completeRecoveryRestore(t.Context(), recoveryRestoreOps{
		Restore: runtime.Restore.Restore,
		Projection: func(ctx context.Context) (app.RecoveryProjection, error) {
			return runtime.CurrentProjection(ctx)
		},
		Quit: func() { quitCalls++ },
	})
	if err == nil || quitCalls != 0 {
		t.Fatalf("production restore error/quit = %v/%d, want error/0", err, quitCalls)
	}
	current, loadErr := store.Load(t.Context(), rolledBack.Identity)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if current.RollbackRestart.ACKPresent {
		t.Fatalf("failed production restore persisted ACK = %#v", current.RollbackRestart.ACK)
	}
}

func createAgentTerminalProductionRecovery(t *testing.T) (*recovery.Store, recovery.Transaction) {
	t.Helper()
	root := mustAgentTerminalValue(t, func() (string, error) {
		return filepath.EvalSymlinks(t.TempDir())
	})
	target := filepath.Join(root, "Super Dolphin.app")
	id := mustAgentTerminalValue(t, recovery.NewTransactionID)
	paths := mustAgentTerminalValue(t, func() (recovery.Paths, error) {
		return recovery.PathsFor(target, id)
	})
	artifact := buildAgentTerminalRollbackArtifact(t)
	writeAgentTerminalRelease(t, target, "old", artifact)
	writeAgentTerminalRelease(t, paths.Staging, "candidate", artifact)
	oldDigest := mustAgentTerminalValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(target)
	})
	candidateDigest := mustAgentTerminalValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(paths.Staging)
	})
	store := mustAgentTerminalValue(t, func() (*recovery.Store, error) {
		return recovery.NewStore(recovery.TransactionRootForTarget(target))
	})
	identity := recovery.Identity{
		TransactionID: id, AttemptID: "agent-terminal-production-callback",
		OldRelease:       recovery.ReleaseIdentity{SHA256: oldDigest, SignerIdentity: "TEAM-OLD"},
		CandidateRelease: recovery.ReleaseIdentity{SHA256: candidateDigest, SignerIdentity: "TEAM-NEW"},
		OldHelpers:       agentTerminalHelperIdentity("old"),
		CandidateHelpers: agentTerminalHelperIdentity("candidate"),
		UpdaterProcess: recovery.ProcessIdentity{
			PID: 101, StartToken: "agent-terminal-updater", ExecutableIdentity: "/test/updater",
			ExecutableSHA256: agentTerminalDigest("updater"),
		},
	}
	transaction := mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return store.Create(t.Context(), recovery.CreateRequest{
			Identity: identity, Paths: paths,
			Trust: recovery.TrustGeneration{
				PreviousGeneration: "trust-old", Generation: "trust-candidate",
				PackageSigner: "TEAM-NEW", State: recovery.TrustPending,
			},
		})
	})
	transaction = mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return store.RetainBackup(t.Context(), transaction.Identity)
	})
	transaction = mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return store.InstallCandidate(t.Context(), transaction.Identity)
	})
	return store, mustAgentTerminalValue(t, func() (recovery.Transaction, error) {
		return store.RollbackUnclaimedProbation(t.Context(), transaction.Identity)
	})
}

func writeAgentTerminalRelease(t *testing.T, root, marker, artifact string) {
	t.Helper()
	executable := filepath.Join(root, "Contents", "MacOS", "agent-terminal")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := mustAgentTerminalValue(t, func() ([]byte, error) { return os.ReadFile(artifact) })
	if err := os.WriteFile(executable, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, marker+".txt"), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
}

func agentTerminalHelperIdentity(label string) recovery.HelperIdentity {
	return recovery.HelperIdentity{
		UpdaterSHA256: agentTerminalDigest(label + "-updater"),
		GuardSHA256:   agentTerminalDigest(label + "-guard"),
	}
}

func agentTerminalDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func mustAgentTerminalValue[T any](t *testing.T, load func() (T, error)) T {
	t.Helper()
	value, err := load()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustAgentTerminalNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
