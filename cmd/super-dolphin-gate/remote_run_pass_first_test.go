package main

import (
	"context"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type remotePreparedExecutionOwnerFunc func(context.Context, remoteci.RunInput, *remoteci.PreparedRun) (remoteci.RunResult, error)

func (execute remotePreparedExecutionOwnerFunc) ExecutePrepared(ctx context.Context, input remoteci.RunInput, prepared *remoteci.PreparedRun) (remoteci.RunResult, error) {
	return execute(ctx, input, prepared)
}

func testRemoteExecutionOwnerFactory(
	run func(*remoteci.Coordinator, context.Context, *remoteci.PreparedRun) (remoteci.RunResult, error),
	finalize func(remoteci.RunInput, *remoteci.RunResult, error) error,
) func(*remoteci.Coordinator) (remotePreparedExecutionOwner, error) {
	return func(coordinator *remoteci.Coordinator) (remotePreparedExecutionOwner, error) {
		return remotePreparedExecutionOwnerFunc(func(ctx context.Context, input remoteci.RunInput, prepared *remoteci.PreparedRun) (remoteci.RunResult, error) {
			result, runErr := run(coordinator, ctx, prepared)
			if err := finalize(input, &result, runErr); err != nil {
				return result, err
			}
			return result, runErr
		}), nil
	}
}

type allHitDependencyProbe struct {
	t                                    *testing.T
	reloadCalls, runCalls, finalizeCalls int
}

// allReused 固定返回严格 all-hit。
func (probe *allHitDependencyProbe) allReused(*remoteci.PreparedRun) bool { return true }

// reloadPlanning 记录 all-hit 的无副作用规划阶段。
func (probe *allHitDependencyProbe) reloadPlanning(remoteRunOptions, remoteci.BaselineState, string, remoteci.RunInput, *remoteci.PreparedRun) error {
	probe.reloadCalls++
	return nil
}

// runPrepared 返回已复用的 PASS。
func (probe *allHitDependencyProbe) runPrepared(*remoteci.Coordinator, context.Context, *remoteci.PreparedRun) (remoteci.RunResult, error) {
	probe.runCalls++
	return remoteci.RunResult{Status: gatecontract.ResultStatusPassed}, nil
}

// finalizeEvidence 验证 all-hit 未被注入 MISS-only identity。
func (probe *allHitDependencyProbe) finalizeEvidence(input remoteci.RunInput, _ *remoteci.RunResult, _ error) error {
	probe.finalizeCalls++
	if input.CandidateGateSourceSHA256 != "" || input.CandidateGateToolchainSHA256 != "" ||
		input.ExecutionImageCacheSnapshotID != "" {
		probe.t.Fatalf("all-hit input gained MISS-only identity: %#v", input)
	}
	return nil
}

// dependencies 组装 all-hit 故障注入边界。
func (probe *allHitDependencyProbe) dependencies() remotePreparedRunDependencies {
	return remotePreparedRunDependencies{
		allReused:      probe.allReused,
		reloadPlanning: probe.reloadPlanning, newExecutionOwner: testRemoteExecutionOwnerFactory(probe.runPrepared, probe.finalizeEvidence),
	}
}

func TestReloadRemotePlanningSkipsCalibrationForSelectedTests(t *testing.T) {
	err := reloadRemotePlanningAfterCalibration(
		remoteRunOptions{}, remoteci.BaselineState{}, "", remoteci.RunInput{SelectedTests: true}, &remoteci.PreparedRun{},
	)
	if err != nil {
		t.Fatalf("selected remote test calibration reload error = %v", err)
	}
}

func TestExecutePreparedRemoteRunAllHitSkipsMissDependencies(t *testing.T) {
	probe := &allHitDependencyProbe{t: t}
	result, _, err := executePreparedRemoteRun(
		context.Background(), remoteRunOptions{}, remoteci.BaselineState{},
		"runner", remoteci.RunInput{}, nil, nil, probe.dependencies(),
	)
	assertAllHitExecution(t, result, err, probe)
}

// TestExecutePreparedRemoteRunRefreshesCalibrationBeforeMissDependencies 验证
// 校准新增 PASS 会在旧 Gate 的 MISS 绑定边界前生效。
func TestExecutePreparedRemoteRunRefreshesCalibrationBeforeMissDependencies(t *testing.T) {
	recovered := false
	dependencies := remotePreparedRunDependencies{
		allReused: func(*remoteci.PreparedRun) bool { return recovered },
		reloadPlanning: func(remoteRunOptions, remoteci.BaselineState, string, remoteci.RunInput, *remoteci.PreparedRun) error {
			recovered = true
			return nil
		},
		newExecutionOwner: testRemoteExecutionOwnerFactory(passedRemotePreparedRun, noOpRemoteEvidenceFinalizer),
	}
	if _, _, err := executePreparedRemoteRun(context.Background(), remoteRunOptions{}, remoteci.BaselineState{}, "runner", remoteci.RunInput{}, nil, nil, dependencies); err != nil {
		t.Fatalf("executePreparedRemoteRun() error = %v", err)
	}
}

// assertAllHitExecution 验证 all-hit 只执行终态链。
func assertAllHitExecution(t *testing.T, result remoteci.RunResult, err error, probe *allHitDependencyProbe) {
	t.Helper()
	if err != nil {
		t.Fatalf("executePreparedRemoteRun() error = %v", err)
	}
	if result.Status != gatecontract.ResultStatusPassed {
		t.Fatalf("all-hit status = %q, want PASS", result.Status)
	}
	if probe.reloadCalls != 1 || probe.runCalls != 1 || probe.finalizeCalls != 1 {
		t.Fatalf("all-hit terminal calls = reload:%d run:%d finalize:%d, want each once", probe.reloadCalls, probe.runCalls, probe.finalizeCalls)
	}
}

// noOpRemoteReloadPlanning 跳过与本断言无关的校准重载，保持 PASS 断言聚焦。
func noOpRemoteReloadPlanning(remoteRunOptions, remoteci.BaselineState, string, remoteci.RunInput, *remoteci.PreparedRun) error {
	return nil
}

// passedRemotePreparedRun 返回测试用 PASS。
func passedRemotePreparedRun(*remoteci.Coordinator, context.Context, *remoteci.PreparedRun) (remoteci.RunResult, error) {
	return remoteci.RunResult{Status: gatecontract.ResultStatusPassed}, nil
}

// noOpRemoteEvidenceFinalizer 跳过与本断言无关的证据写入。
func noOpRemoteEvidenceFinalizer(remoteci.RunInput, *remoteci.RunResult, error) error { return nil }

func TestExecutePreparedRemoteRunPartialMissFailsFastWithoutImageCacheSuccessor(t *testing.T) {
	dependencies := remotePreparedRunDependencies{
		allReused:         func(*remoteci.PreparedRun) bool { return false },
		reloadPlanning:    noOpRemoteReloadPlanning,
		newExecutionOwner: testRemoteExecutionOwnerFactory(passedRemotePreparedRun, noOpRemoteEvidenceFinalizer),
	}
	_, _, err := executePreparedRemoteRun(
		context.Background(), remoteRunOptions{}, remoteci.BaselineState{},
		"runner", remoteci.RunInput{}, nil, nil, dependencies,
	)
	if err == nil || !strings.Contains(err.Error(), "ImageCache execution is retired") {
		t.Fatalf("partial MISS error = %v, want retired ImageCache fail-fast", err)
	}
}
