package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	recovery "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/appupdaterecovery"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

// TestMain 让测试二进制在 helper 模式下只处理一次 filesystem 请求。
func TestMain(m *testing.M) {
	if handled, err := recovery.RunReleaseFilesystemHelperIfRequested(os.Stdin, os.Stdout); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRecoveryGraphContainsOnlyAllowedConstructors(t *testing.T) {
	want := []string{
		"recovery.state",
		"recovery.check",
		"recovery.retry",
		"recovery.restore",
	}
	got := RecoveryConstructorIDs()
	if !slices.Equal(got, want) {
		t.Fatalf("RecoveryConstructorIDs() = %v, want %v", got, want)
	}
	if err := ValidateRecoveryConstructors(got); err != nil {
		t.Fatalf("ValidateRecoveryConstructors() error = %v", err)
	}
}

func TestRecoveryProjectionFieldGuardEnumeratesProducerFields(t *testing.T) {
	producer := reflect.TypeFor[RecoveryProjection]()
	seen := make(map[string]struct{}, producer.NumField())
	for index := 0; index < producer.NumField(); index++ {
		field := producer.Field(index)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if field.PkgPath != "" || name == "" || name == "-" {
			t.Fatalf("producer=%s field=%s is not serialized", producer.Name(), field.Name)
		}
		if _, exists := seen[name]; exists {
			t.Fatalf("producer=%s field=%s is duplicated", producer.Name(), name)
		}
		seen[name] = struct{}{}
	}
}

func TestRecoveryProjectionReasonDoesNotPublishRawCause(t *testing.T) {
	const rawCause = "secret=sk-recovery path=/private/recovery dsn=postgres://private"
	projection := projectRecoveryTransaction(recovery.Transaction{}, rawCause)
	for _, sensitive := range []string{"secret=sk-recovery", "/private/recovery", "dsn=postgres://private"} {
		if strings.Contains(projection.Reason, sensitive) {
			t.Fatalf("Recovery projection reason leaked %q: %q", sensitive, projection.Reason)
		}
	}
	if !strings.HasPrefix(projection.Reason, "RECOVERY_STARTUP_FAILED|") {
		t.Fatalf("Recovery projection reason = %q, want stable public code", projection.Reason)
	}
}

func TestNewRecoveryPublicFailureRejectsInvalidInput(t *testing.T) {
	const rawCause = "secret=sk-recovery path=/private/recovery dsn=postgres://private"
	for _, test := range []struct {
		name  string
		code  RecoveryPublicErrorCode
		cause error
	}{
		{name: "nil_cause", code: RecoveryPublicCodeStartupFailed},
		{name: "unallowlisted_code", code: RecoveryPublicErrorCode(rawCause), cause: errors.New(rawCause)},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure, err := NewRecoveryPublicFailure(test.code, test.cause)
			if err == nil {
				t.Fatalf("NewRecoveryPublicFailure() = %#v, want explicit invalid-input error", failure)
			}
			if strings.Contains(err.Error(), rawCause) {
				t.Fatalf("NewRecoveryPublicFailure() error leaked raw cause: %q", err)
			}
		})
	}
}

func TestSelectStartupDoesNotRecordHealthyACKBeforeReady(t *testing.T) {
	store, transaction, process := createStartupProbation(t)
	selection, err := SelectStartup(context.Background(), StartupSelectorInput{
		Store: store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
		LeaseWait: time.Second, DigestTimeout: StartupDigestTimeout,
	})
	if err != nil {
		t.Fatalf("SelectStartup() error = %v", err)
	}
	if !selection.HasActiveProbation() {
		t.Fatal("selection does not retain exact active probation")
	}
	beforeReady, err := store.Load(context.Background(), transaction.Identity)
	if err != nil {
		t.Fatalf("Load(before ready) error = %v", err)
	}
	if beforeReady.Probation.ACKPresent {
		t.Fatal("selector recorded healthy ACK before desktop readiness")
	}
	readyAt := time.Now()
	if err := selection.RecordReadyACK(context.Background(), readyAt); err != nil {
		t.Fatalf("RecordReadyACK() error = %v", err)
	}
	afterReady, err := store.Load(context.Background(), transaction.Identity)
	if err != nil {
		t.Fatalf("Load(after ready) error = %v", err)
	}
	if !afterReady.Probation.ACKPresent || afterReady.Probation.ACK.Process != process {
		t.Fatalf("ready ACK = %#v, want exact process %#v", afterReady.Probation, process)
	}
}

func TestSelectStartupRejectsNonPositiveDigestTimeout(t *testing.T) {
	store, transaction, process := createStartupProbation(t)
	for _, digestTimeout := range []time.Duration{0, -time.Second} {
		_, err := SelectStartup(context.Background(), StartupSelectorInput{
			Store: store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
			LeaseWait: time.Second, DigestTimeout: digestTimeout,
		})
		if err == nil || !strings.Contains(err.Error(), "startup digest timeout must be positive") {
			t.Fatalf("SelectStartup(DigestTimeout=%s) error = %v, want positive timeout error", digestTimeout, err)
		}
	}
}

func TestSelectStartupAuthenticatedRolledBackLaunchEntersNormal(t *testing.T) {
	store, transaction, process := createRolledBackStartup(t)
	selection, err := SelectStartup(t.Context(), StartupSelectorInput{
		Store: store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
		RollbackLaunch: true, DigestTimeout: StartupDigestTimeout,
	})
	if err != nil {
		t.Fatalf("SelectStartup() error = %v", err)
	}
	if selection.Mode != StartupModeNormal || selection.Transaction.State != recovery.StateRolledBack {
		t.Fatalf("selection = %#v, want authenticated rolled_back Normal", selection)
	}
}

func TestSelectStartupRolledBackLaunchIdentityMismatchFailsFast(t *testing.T) {
	store, transaction, process := createRolledBackStartup(t)
	tests := []struct {
		name   string
		mutate func(*StartupSelectorInput)
	}{
		{name: "purpose", mutate: func(input *StartupSelectorInput) { input.RollbackLaunch = false }},
		{name: "transaction", mutate: func(input *StartupSelectorInput) {
			input.ExpectedTransactionID = recovery.TransactionID(strings.Repeat("f", 32))
		}},
		{name: "token", mutate: func(input *StartupSelectorInput) {
			input.Process.TerminationToken = strings.Repeat("b", 64)
		}},
		{name: "process", mutate: func(input *StartupSelectorInput) { input.Process.StartToken += "-mismatch" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := StartupSelectorInput{
				Store: store, Process: process, ExpectedTransactionID: transaction.Identity.TransactionID,
				RollbackLaunch: true, DigestTimeout: StartupDigestTimeout,
			}
			test.mutate(&input)
			selection, err := SelectStartup(t.Context(), input)
			if err == nil || selection.Mode != StartupModeRecovery {
				t.Fatalf("SelectStartup() selection=%#v error=%v, want fail-fast Recovery", selection, err)
			}
		})
	}
}

func TestRecoveryRestoreClosesStableCrashBetweenCallsStates(t *testing.T) {
	for _, retainBackup := range []bool{false, true} {
		name := "prepared"
		if retainBackup {
			name = "backup_retained"
		}
		t.Run(name, func(t *testing.T) {
			store, transaction := createStableRecoveryTransaction(t, retainBackup)
			runtime, err := NewRecoveryRuntime(StartupSelection{
				Mode: StartupModeRecovery, Store: store, Transaction: transaction,
				Projection: projectRecoveryTransaction(transaction, "updater interrupted"),
			})
			if err != nil {
				t.Fatalf("NewRecoveryRuntime() error = %v", err)
			}
			runtime.Restore.restartCallbacks = successfulRecoveryRestartCallbacks
			restored, err := runtime.Restore.Restore(context.Background())
			if err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if restored.State != recovery.StateRolledBack || !restored.RollbackRestart.ACKPresent {
				t.Fatalf("restored state = %q", restored.State)
			}
			contents, err := os.ReadFile(restored.Paths.Target)
			if err != nil || string(contents) != "old" {
				t.Fatalf("restored target contents=%q error=%v", contents, err)
			}
		})
	}
}

func TestRecoveryRestoreRollsBackUnclaimedProbation(t *testing.T) {
	store, transaction := createStableRecoveryTransaction(t, true)
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.InstallCandidate(context.Background(), transaction.Identity)
	})
	if transaction.State != recovery.StateProbation || transaction.Probation.LeasePresent {
		t.Fatalf("unclaimed probation fixture = %#v", transaction)
	}
	runtime, err := NewRecoveryRuntime(StartupSelection{
		Mode: StartupModeRecovery, Store: store, Transaction: transaction,
		Projection: projectRecoveryTransaction(transaction, "updater interrupted before lease"),
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	runtime.Restore.restartCallbacks = successfulRecoveryRestartCallbacks
	restored, err := runtime.Restore.Restore(context.Background())
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.State != recovery.StateRolledBack || restored.Probation.LeasePresent || !restored.RollbackRestart.ACKPresent {
		t.Fatalf("restored transaction = %#v", restored)
	}
	contents, err := os.ReadFile(restored.Paths.Target)
	if err != nil || string(contents) != "old" {
		t.Fatalf("restored target contents=%q error=%v", contents, err)
	}
}

func TestRecoveryRestoreForeignLeaseStopsExactBeforeRollback(t *testing.T) {
	store, transaction, foreign := createStartupProbation(t)
	current := foreign
	current.PID++
	runtime, err := NewRecoveryRuntime(StartupSelection{
		Mode: StartupModeRecovery, Store: store, Transaction: transaction,
		Projection: projectRecoveryTransaction(transaction, "foreign probation lease"),
		process:    current,
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	stopped := false
	runtime.Restore.terminateProcess = func(ctx context.Context, process recovery.ProcessIdentity) error {
		if process != foreign {
			t.Fatalf("terminate process = %#v, want foreign lease process %#v", process, foreign)
		}
		before := mustStartupValue(t, func() (recovery.Transaction, error) {
			return store.Load(ctx, transaction.Identity)
		})
		if before.State != recovery.StateProbation {
			t.Fatalf("journal state before exact stop = %q, want probation", before.State)
		}
		stopped = true
		return nil
	}
	runtime.Restore.restartCallbacks = successfulRecoveryRestartCallbacks
	restored, err := runtime.Restore.Restore(t.Context())
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if !stopped || restored.State != recovery.StateRolledBack {
		t.Fatalf("Restore() stopped=%t state=%q, want true/rolled_back", stopped, restored.State)
	}
}

func TestRecoveryRestoreForeignLeaseStopFailureLeavesStateUntouched(t *testing.T) {
	store, transaction, foreign := createStartupProbation(t)
	current := foreign
	current.StartToken = "different-current-process"
	stopErr := errors.New("authenticated exact stop failed")
	runtime, err := NewRecoveryRuntime(StartupSelection{
		Mode: StartupModeRecovery, Store: store, Transaction: transaction,
		Projection: projectRecoveryTransaction(transaction, "foreign probation lease"),
		process:    current,
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	runtime.Restore.terminateProcess = func(context.Context, recovery.ProcessIdentity) error { return stopErr }
	runtime.Restore.restartCallbacks = successfulRecoveryRestartCallbacks
	beforeDigest := mustStartupValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(transaction.Paths.Target)
	})
	_, err = runtime.Restore.Restore(t.Context())
	if !errors.Is(err, stopErr) {
		t.Fatalf("Restore() error = %v, want %v", err, stopErr)
	}
	after := mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.Load(t.Context(), transaction.Identity)
	})
	afterDigest := mustStartupValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(transaction.Paths.Target)
	})
	if !reflect.DeepEqual(after, transaction) || afterDigest != beforeDigest {
		t.Fatalf("stop failure mutated recovery state: transaction=%#v digest=%q", after, afterDigest)
	}
}

func TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch(t *testing.T) {
	store, transaction := createStableRecoveryTransaction(t, true)
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.InstallCandidate(t.Context(), transaction.Identity)
	})
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.RollbackUnclaimedProbation(t.Context(), transaction.Identity)
	})
	if transaction.State != recovery.StateRolledBack || transaction.RollbackRestart.LaunchToken == "" {
		t.Fatalf("rolled back transaction = %#v", transaction)
	}
	fixture := &recoveryGuardRestartFixture{transaction: transaction, process: recovery.RollbackRestartProcess{
		PID: 303, StartToken: "recovery-guard-start",
		ExecutableIdentity: transaction.Paths.Target + "/Contents/MacOS/agent-terminal",
		ExecutableSHA256:   transaction.Identity.OldRelease.SHA256,
	}}
	runtime, err := NewRecoveryRuntime(StartupSelection{
		Mode: StartupModeRecovery, Store: store, Transaction: transaction,
		Projection: projectRecoveryTransaction(transaction, "Recovery and Guard convergence"),
	})
	if err != nil {
		t.Fatalf("NewRecoveryRuntime() error = %v", err)
	}
	runtime.Restore.restartCallbacks = fixture.callbacks
	guardResolve, guardLaunch := fixture.callbacks(transaction)
	start := make(chan struct{})
	results := make(chan error, 2)
	runtimesafe.SafeGo(t.Context(), nil, "app.recoveryRestore.concurrent", func(context.Context) {
		<-start
		_, restoreErr := runtime.Restore.Restore(t.Context())
		results <- restoreErr
	})
	runtimesafe.SafeGo(t.Context(), nil, "app.recoveryGuard.concurrent", func(context.Context) {
		<-start
		_, guardErr := store.ConvergeRollbackRestart(t.Context(), transaction.Identity, guardResolve, guardLaunch)
		results <- guardErr
	})
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent Recovery/Guard convergence error = %v", err)
		}
	}

	repeated, err := runtime.Restore.Restore(t.Context())
	if err != nil {
		t.Fatalf("repeated Recovery Restore() error = %v", err)
	}
	assertRecoveryGuardRestartConverged(t, fixture, transaction, repeated)
}

func assertRecoveryGuardRestartConverged(
	t *testing.T,
	fixture *recoveryGuardRestartFixture,
	want recovery.Transaction,
	got recovery.Transaction,
) {
	t.Helper()
	if fixture.launches != 1 {
		t.Fatalf("launches = %d, want 1", fixture.launches)
	}
	if fixture.resolves < 4 || !fixture.live {
		t.Fatalf("resolves/live = %d/%t, want >=4/true", fixture.resolves, fixture.live)
	}
	if !got.RollbackRestart.ACKPresent {
		t.Fatal("rollback restart ACK is absent")
	}
	if got.RollbackRestart.ACK.LaunchToken != want.RollbackRestart.LaunchToken {
		t.Fatalf("ACK launch token = %q, want %q", got.RollbackRestart.ACK.LaunchToken, want.RollbackRestart.LaunchToken)
	}
	if got.RollbackRestart.ACK.Process != fixture.process {
		t.Fatalf("rollback restart ACK process = %#v, want %#v", got.RollbackRestart.ACK.Process, fixture.process)
	}
}

func successfulRecoveryRestartCallbacks(transaction recovery.Transaction) (
	recovery.RollbackRestartResolver,
	recovery.RollbackRestartLauncher,
) {
	fixture := &recoveryGuardRestartFixture{transaction: transaction, process: recovery.RollbackRestartProcess{
		PID: 202, StartToken: "recovery-restore-start",
		ExecutableIdentity: transaction.Paths.Target + "/Contents/MacOS/agent-terminal",
		ExecutableSHA256:   transaction.Identity.OldRelease.SHA256,
	}}
	return fixture.callbacks(transaction)
}

type recoveryGuardRestartFixture struct {
	transaction recovery.Transaction
	process     recovery.RollbackRestartProcess
	launches    int
	resolves    int
	live        bool
}

func (fixture *recoveryGuardRestartFixture) callbacks(recovery.Transaction) (
	recovery.RollbackRestartResolver,
	recovery.RollbackRestartLauncher,
) {
	return fixture.resolve, fixture.launch
}

func (fixture *recoveryGuardRestartFixture) control() recovery.RollbackRestartControl {
	return recovery.RollbackRestartControl{
		Process: fixture.process, Cleanup: func() error { fixture.live = false; return nil },
		Prepare:  func(context.Context) error { return nil },
		Activate: func(context.Context) error { return nil },
	}
}

func (fixture *recoveryGuardRestartFixture) resolve(
	_ context.Context,
	token string,
) (recovery.RollbackRestartControl, bool, error) {
	fixture.resolves++
	if token != fixture.transaction.RollbackRestart.LaunchToken {
		return recovery.RollbackRestartControl{}, false, errors.New("resolver received non-durable launch token")
	}
	return fixture.control(), fixture.live, nil
}

func (fixture *recoveryGuardRestartFixture) launch(
	_ context.Context,
	token string,
) (recovery.RollbackRestartControl, error) {
	if token != fixture.transaction.RollbackRestart.LaunchToken {
		return recovery.RollbackRestartControl{}, errors.New("launcher received non-durable launch token")
	}
	if fixture.live {
		return recovery.RollbackRestartControl{}, errors.New("duplicate rolled back release launch")
	}
	fixture.launches++
	fixture.live = true
	return fixture.control(), nil
}

func createRolledBackStartup(t *testing.T) (*recovery.Store, recovery.Transaction, recovery.ProcessIdentity) {
	t.Helper()
	store, transaction := createStableRecoveryTransaction(t, true)
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.InstallCandidate(t.Context(), transaction.Identity)
	})
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.RollbackUnclaimedProbation(t.Context(), transaction.Identity)
	})
	resolve, launch := successfulRecoveryRestartCallbacks(transaction)
	transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.ConvergeRollbackRestart(t.Context(), transaction.Identity, resolve, launch)
	})
	token := transaction.RollbackRestart.LaunchToken
	process := transaction.RollbackRestart.ACK.Process
	return store, transaction, recovery.ProcessIdentity{
		PID: process.PID, StartToken: process.StartToken, ExecutableIdentity: process.ExecutableIdentity,
		ExecutableSHA256: process.ExecutableSHA256,
		TerminationEndpoint: filepath.Join(os.TempDir(), fmt.Sprintf(
			"sd-rr-%s-%s.sock", string(transaction.Identity.TransactionID)[:8], token[:16],
		)),
		TerminationToken: token,
	}
}

func createStableRecoveryTransaction(t *testing.T, retainBackup bool) (*recovery.Store, recovery.Transaction) {
	t.Helper()
	parent := t.TempDir()
	store := mustStartupValue(t, func() (*recovery.Store, error) {
		return recovery.NewStore(filepath.Join(parent, ".transactions"))
	})
	id := mustStartupValue(t, recovery.NewTransactionID)
	paths := mustStartupValue(t, func() (recovery.Paths, error) {
		return recovery.PathsFor(filepath.Join(parent, "Super Dolphin.app"), id)
	})
	mustStartupNoError(t, "write old release", os.WriteFile(paths.Target, []byte("old"), 0o600))
	mustStartupNoError(t, "write candidate release", os.WriteFile(paths.Staging, []byte("candidate"), 0o600))
	oldDigest := mustStartupValue(t, func() (string, error) { return recovery.ComputeReleaseDigest(paths.Target) })
	candidateDigest := mustStartupValue(t, func() (string, error) { return recovery.ComputeReleaseDigest(paths.Staging) })
	identity := recovery.Identity{
		TransactionID: id, AttemptID: "stable-recovery",
		OldRelease:       recovery.ReleaseIdentity{SHA256: oldDigest, SignerIdentity: "TEAM-OLD"},
		CandidateRelease: recovery.ReleaseIdentity{SHA256: candidateDigest, SignerIdentity: "TEAM-NEW"},
		OldHelpers:       recoveryGraphHelpers("old"), CandidateHelpers: recoveryGraphHelpers("candidate"),
		UpdaterProcess: recoveryGraphUpdaterProcess(),
	}
	transaction := mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.Create(context.Background(), recovery.CreateRequest{
			Identity: identity, Paths: paths,
			Trust: recovery.TrustGeneration{PreviousGeneration: "trust-old", Generation: "trust-stable", PackageSigner: "TEAM-NEW", State: recovery.TrustPending},
		})
	})
	if retainBackup {
		transaction = mustStartupValue(t, func() (recovery.Transaction, error) {
			return store.RetainBackup(context.Background(), identity)
		})
	}
	return store, transaction
}

func createStartupProbation(t *testing.T) (*recovery.Store, recovery.Transaction, recovery.ProcessIdentity) {
	t.Helper()
	parent := t.TempDir()
	store := mustStartupValue(t, func() (*recovery.Store, error) {
		return recovery.NewStore(filepath.Join(parent, ".transactions"))
	})
	transactionID := mustStartupValue(t, recovery.NewTransactionID)
	paths := mustStartupValue(t, func() (recovery.Paths, error) {
		return recovery.PathsFor(filepath.Join(parent, "Super Dolphin.app"), transactionID)
	})
	mustStartupNoError(t, "write old release", os.WriteFile(paths.Target, []byte("old"), 0o600))
	mustStartupNoError(t, "write candidate release", os.WriteFile(paths.Staging, []byte("candidate"), 0o600))
	oldDigest := mustStartupValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(paths.Target)
	})
	candidateDigest := mustStartupValue(t, func() (string, error) {
		return recovery.ComputeReleaseDigest(paths.Staging)
	})
	identity := recovery.Identity{
		TransactionID: transactionID,
		AttemptID:     "ready-sequence",
		OldRelease: recovery.ReleaseIdentity{
			SHA256: oldDigest, SignerIdentity: "TEAM-OLD",
		},
		CandidateRelease: recovery.ReleaseIdentity{
			SHA256: candidateDigest, SignerIdentity: "TEAM-NEW",
		},
		OldHelpers: recoveryGraphHelpers("old"), CandidateHelpers: recoveryGraphHelpers("candidate"),
		UpdaterProcess: recoveryGraphUpdaterProcess(),
	}
	mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.Create(context.Background(), recovery.CreateRequest{
			Identity: identity,
			Paths:    paths,
			Trust: recovery.TrustGeneration{
				PreviousGeneration: "trust-old",
				Generation:         "trust-ready", PackageSigner: "TEAM-NEW", State: recovery.TrustPending,
			},
		})
	})
	mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.RetainBackup(context.Background(), identity)
	})
	mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.InstallCandidate(context.Background(), identity)
	})
	process := recovery.ProcessIdentity{
		PID: 42, StartToken: "ready-start", ExecutableIdentity: "/candidate",
		ExecutableSHA256: candidateDigest, TerminationEndpoint: filepath.Join(parent, "candidate.sock"),
		TerminationToken: strings.Repeat("a", 64),
	}
	mustStartupValue(t, func() (recovery.ProbationLease, error) {
		return store.AcquireProbationLease(context.Background(), identity, recovery.ProbationLeaseRequest{
			OwnerID: "updater-ready", Process: process, TTL: time.Minute,
		})
	})
	transaction := mustStartupValue(t, func() (recovery.Transaction, error) {
		return store.Load(context.Background(), identity)
	})
	return store, transaction, process
}

func recoveryGraphHelpers(label string) recovery.HelperIdentity {
	digest := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	return recovery.HelperIdentity{UpdaterSHA256: digest(label + " updater"), GuardSHA256: digest(label + " guard")}
}

func recoveryGraphUpdaterProcess() recovery.ProcessIdentity {
	sum := sha256.Sum256([]byte("updater"))
	return recovery.ProcessIdentity{
		PID: 99, StartToken: "recovery-updater", ExecutableIdentity: "/test/updater",
		ExecutableSHA256: hex.EncodeToString(sum[:]),
	}
}

func mustStartupValue[T any](t *testing.T, load func() (T, error)) T {
	t.Helper()
	value, err := load()
	if err != nil {
		t.Fatalf("startup fixture error = %v", err)
	}
	return value
}

func mustStartupNoError(t *testing.T, label string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
}
