package appupdaterecovery

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

// RollbackRestartCallbacks 构造按 durable launch token 重发现或启动旧版本的共享回调。
func RollbackRestartCallbacks(transaction Transaction) (RollbackRestartResolver, RollbackRestartLauncher) {
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
		process, err := rollbackRestartProcess(stable, executable)
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
		cmd := exec.Command(executable, argument(token))
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			return RollbackRestartProcess{}, err
		}
		stable, err := pidregistry.CaptureStableProcessIdentity(cmd.Process.Pid)
		if err != nil {
			_ = cmd.Process.Kill()
			return RollbackRestartProcess{}, err
		}
		process, err := rollbackRestartProcess(stable, executable)
		if err != nil {
			_ = cmd.Process.Kill()
			return RollbackRestartProcess{}, err
		}
		if err := cmd.Process.Release(); err != nil {
			return RollbackRestartProcess{}, err
		}
		return process, nil
	}
	return resolve, launch
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
