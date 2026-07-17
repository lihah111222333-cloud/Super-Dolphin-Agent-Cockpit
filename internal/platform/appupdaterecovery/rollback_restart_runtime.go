package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

type rollbackRestartRuntime struct {
	start    func(string, string, []string) (*exec.Cmd, error)
	capture  func(int) (pidregistry.StableProcessIdentity, error)
	validate func(pidregistry.StableProcessIdentity, string) (RollbackRestartProcess, error)
	release  func(*os.Process) error
	kill     func(*os.Process) error
	wait     func(*exec.Cmd) error
}

// RollbackRestartCallbacks 构造按 durable launch token 重发现或启动旧版本的共享回调。
func RollbackRestartCallbacks(transaction Transaction) (RollbackRestartResolver, RollbackRestartLauncher) {
	return rollbackRestartCallbacksWithRuntime(transaction, rollbackRestartRuntime{
		start: func(executable, argument string, env []string) (*exec.Cmd, error) {
			cmd := exec.Command(executable, argument)
			cmd.Env = env
			return cmd, cmd.Start()
		},
		capture:  pidregistry.CaptureStableProcessIdentity,
		validate: rollbackRestartProcess,
		release:  (*os.Process).Release,
		kill:     (*os.Process).Kill,
		wait:     (*exec.Cmd).Wait,
	})
}

// rollbackRestartCallbacksWithRuntime 通过可注入进程原语复用生产 launcher 状态机。
func rollbackRestartCallbacksWithRuntime(transaction Transaction, runtime rollbackRestartRuntime) (RollbackRestartResolver, RollbackRestartLauncher) {
	executable := filepath.Join(transaction.Paths.Target, "Contents", "MacOS", "agent-terminal")
	argument := func(token string) string { return "--super-dolphin-rollback-launch-token=" + token }
	resolve := func(token string) (RollbackRestartProcess, bool, error) {
		if err := verifyRolledBackRelease(transaction); err != nil {
			return RollbackRestartProcess{}, false, err
		}
		stable, found, err := pidregistry.FindStableProcessByArgument(argument(token), executable)
		if err != nil || !found {
			return RollbackRestartProcess{}, found, err
		}
		process, err := runtime.validate(stable, executable)
		return process, err == nil, err
	}
	launch := func(token string) (RollbackRestartProcess, error) {
		if err := verifyRolledBackRelease(transaction); err != nil {
			return RollbackRestartProcess{}, err
		}
		env, err := runtimeenv.DetachedCommandEnvironment(os.Environ())
		if err != nil {
			return RollbackRestartProcess{}, err
		}
		cmd, err := runtime.start(executable, argument(token), env)
		if err != nil {
			return RollbackRestartProcess{}, err
		}
		stable, err := runtime.capture(cmd.Process.Pid)
		if err != nil {
			return RollbackRestartProcess{}, cleanupStartedRollbackProcess(runtime, cmd, err)
		}
		process, err := runtime.validate(stable, executable)
		if err != nil {
			return RollbackRestartProcess{}, cleanupStartedRollbackProcess(runtime, cmd, err)
		}
		if err := runtime.release(cmd.Process); err != nil {
			return RollbackRestartProcess{}, cleanupStartedRollbackProcess(runtime, cmd, err)
		}
		return process, nil
	}
	return resolve, launch
}

func cleanupStartedRollbackProcess(runtime rollbackRestartRuntime, cmd *exec.Cmd, primary error) error {
	killErr := runtime.kill(cmd.Process)
	if killErr != nil {
		killErr = fmt.Errorf("kill started rollback process: %w", killErr)
	}
	waitErr := runtime.wait(cmd)
	if waitErr != nil {
		waitErr = fmt.Errorf("wait started rollback process: %w", waitErr)
	}
	return errors.Join(primary, killErr, waitErr)
}

func verifyRolledBackRelease(transaction Transaction) error {
	canonical, err := CanonicalExistingPath(transaction.Paths.Target)
	if err != nil {
		return err
	}
	if canonical != transaction.Paths.Target {
		return errors.New("rolled back target is not canonical")
	}
	digest, err := ComputeReleaseDigest(transaction.Paths.Target)
	if err != nil {
		return err
	}
	if digest != transaction.Identity.OldRelease.SHA256 {
		return errors.New("rolled back target digest does not match old release")
	}
	return nil
}

func rollbackRestartProcess(stable pidregistry.StableProcessIdentity, expectedExecutable string) (RollbackRestartProcess, error) {
	canonical, err := CanonicalExistingPath(stable.ExecutableIdentity)
	if err != nil {
		return RollbackRestartProcess{}, err
	}
	if canonical != expectedExecutable {
		return RollbackRestartProcess{}, pidregistry.ErrStableProcessIdentityMismatch
	}
	digest, err := ComputeReleaseDigest(canonical)
	if err != nil {
		return RollbackRestartProcess{}, err
	}
	return RollbackRestartProcess{
		PID: stable.PID, StartToken: stable.ProcessStartToken,
		ExecutableIdentity: canonical, ExecutableSHA256: digest,
	}, nil
}
