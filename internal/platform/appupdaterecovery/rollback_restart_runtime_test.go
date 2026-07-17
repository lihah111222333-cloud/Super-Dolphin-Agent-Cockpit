package appupdaterecovery

import (
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

func TestRollbackRestartLauncherReapsEveryPostStartFailure(t *testing.T) {
	for _, failure := range []string{"capture", "validation", "release"} {
		t.Run(failure, func(t *testing.T) { runRollbackRestartLauncherFailure(t, failure) })
	}
}

func runRollbackRestartLauncherFailure(t *testing.T, failure string) {
	store, identity, _ := createProbationTransaction(t)
	transaction, err := store.RollbackUnclaimedProbation(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	transaction.Paths.Target, err = CanonicalExistingPath(transaction.Paths.Target)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &rollbackRuntimeFailureFixture{
		failure: failure, primary: errors.New(failure + " failed"),
		killFailure: errors.New("kill evidence"), waitFailure: errors.New("wait evidence"),
	}
	runtime := rollbackRestartRuntime{
		start: fixture.start, capture: fixture.capture, validate: fixture.validate,
		release: fixture.release, kill: fixture.kill, wait: fixture.wait,
	}
	_, launch := rollbackRestartCallbacksWithRuntime(transaction, runtime)
	_, launchErr := launch(transaction.RollbackRestart.LaunchToken)
	for _, want := range []error{fixture.primary, fixture.killFailure, fixture.waitFailure} {
		if !errors.Is(launchErr, want) {
			t.Fatalf("launch error %v does not retain %v", launchErr, want)
		}
	}
	if fixture.started == nil || fixture.started.ProcessState == nil {
		t.Fatal("started rollback process was not waited and reaped")
	}
	if _, err := pidregistry.CaptureStableProcessIdentity(fixture.started.Process.Pid); !errors.Is(err, pidregistry.ErrStableProcessNotFound) {
		t.Fatalf("started rollback process remains observable: %v", err)
	}
}

type rollbackRuntimeFailureFixture struct {
	failure                  string
	primary                  error
	killFailure, waitFailure error
	started                  *exec.Cmd
}

func (fixture *rollbackRuntimeFailureFixture) start(string, string, []string) (*exec.Cmd, error) {
	fixture.started = exec.Command(os.Args[0], "-test.run=TestRollbackRestartRuntimeChild")
	fixture.started.Env = append(os.Environ(), "SUPER_DOLPHIN_TEST_ROLLBACK_RUNTIME_CHILD=1")
	return fixture.started, fixture.started.Start()
}

func (fixture *rollbackRuntimeFailureFixture) capture(pid int) (pidregistry.StableProcessIdentity, error) {
	if fixture.failure == "capture" {
		return pidregistry.StableProcessIdentity{}, fixture.primary
	}
	return pidregistry.StableProcessIdentity{PID: pid, ProcessStartToken: "start", ExecutableIdentity: "executable"}, nil
}

func (fixture *rollbackRuntimeFailureFixture) validate(pidregistry.StableProcessIdentity, string) (RollbackRestartProcess, error) {
	if fixture.failure == "validation" {
		return RollbackRestartProcess{}, fixture.primary
	}
	return fixtureRollbackRestartProcess(), nil
}

func (fixture *rollbackRuntimeFailureFixture) release(*os.Process) error {
	if fixture.failure == "release" {
		return fixture.primary
	}
	return nil
}

func (fixture *rollbackRuntimeFailureFixture) kill(process *os.Process) error {
	return errors.Join(process.Kill(), fixture.killFailure)
}

func (fixture *rollbackRuntimeFailureFixture) wait(cmd *exec.Cmd) error {
	return errors.Join(cmd.Wait(), fixture.waitFailure)
}

func TestRollbackRestartRuntimeChild(t *testing.T) {
	if os.Getenv("SUPER_DOLPHIN_TEST_ROLLBACK_RUNTIME_CHILD") != "1" {
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}
