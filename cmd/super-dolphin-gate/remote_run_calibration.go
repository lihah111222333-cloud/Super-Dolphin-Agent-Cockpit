package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// runRemoteCalibration 执行首代 commit、push 与 release 权威运行并接受完整时长校准。
func runRemoteCalibration(args []string, stdout io.Writer) error {
	options, err := parseRemoteRunOptions(args)
	if err != nil {
		return err
	}
	return withRemoteCalibrationLock(options.LedgerPath, func() error {
		return executeRemoteCalibration(options, stdout)
	})
}

// executeRemoteCalibration 在单飞锁内执行或恢复一次完整校准。
func executeRemoteCalibration(options remoteRunOptions, stdout io.Writer) error {
	if err := validateRemoteCalibrationOptions(options); err != nil {
		return err
	}
	ledgerStore, err := prepareRemoteCalibrationLedger(options.LedgerPath)
	if err != nil {
		return protocolError("prepare remote calibration ledger: %v", err)
	}
	identity, err := resolveRemoteCalibrationIdentity(options.RepositoryRoot, options.Commit)
	if err != nil {
		return err
	}
	checkpoint, err := newRemoteCalibrationCheckpoint(options, identity, ledgerStore)
	if err != nil {
		return infrastructureError("prepare remote calibration checkpoint: %v", err)
	}
	inputs, results, err := executeRemoteCalibrationRuns(options, identity, ledgerStore, checkpoint, stdout)
	if err != nil {
		return err
	}
	if err := acceptAndEmitRemoteCalibration(stdout, ledgerStore, identity.commit, inputs, results); err != nil {
		return err
	}
	if err := checkpoint.Remove(); err != nil {
		return infrastructureError("remove completed remote calibration checkpoint: %v", err)
	}
	return nil
}

// newRemoteCalibrationCheckpoint 将 source、runner 与工具链固定为可恢复校准身份。
func newRemoteCalibrationCheckpoint(
	options remoteRunOptions,
	source remoteCalibrationIdentity,
	ledgerStore *gatecontract.DurationLedgerStore,
) (*remoteci.CalibrationCheckpoint, error) {
	if ledgerStore == nil {
		return nil, errors.New("remote calibration ledger store is required")
	}
	state, err := loadAcceptedRemoteBaseline(options.ConfigPath, options.StatePath, options.LedgerPath)
	if err != nil {
		return nil, err
	}
	runnerIdentity, err := resolveRemoteRunnerIdentity(options.RepositoryRoot, state)
	if err != nil {
		return nil, err
	}
	identity := remoteCalibrationCheckpointIdentity(source, state, runnerIdentity)
	return remoteci.NewCalibrationCheckpoint(
		ledgerStore,
		identity,
	)
}

// remoteCalibrationCheckpointIdentity 隔离不同候选、平台、runner 与工具链的断点。
func remoteCalibrationCheckpointIdentity(
	source remoteCalibrationIdentity,
	state remoteci.BaselineState,
	runnerIdentity string,
) string {
	material := strings.Join([]string{
		"super-dolphin-remote-calibration-checkpoint-v2",
		source.commit, source.tree, source.base, state.Platform,
		runnerIdentity, state.ToolchainDigest,
	}, "\x00")
	sum := sha256.Sum256([]byte(material))
	return fmt.Sprintf("sha256:%x", sum[:])
}

// ensureRemoteDurationCalibration 在普通远程运行前自动补齐当前 runner 的单飞校准。
