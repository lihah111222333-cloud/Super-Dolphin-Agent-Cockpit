package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

type allHitDependencyProbe struct {
	t                                     *testing.T
	closureCalls, runtimeCalls, bindCalls int
	reloadCalls, runCalls, finalizeCalls  int
}

// allReused 固定返回严格 all-hit。
func (probe *allHitDependencyProbe) allReused(*remoteci.PreparedRun) bool { return true }

// resolveCandidateGateIdentity 记录任何不应发生的候选闭包读取。
func (probe *allHitDependencyProbe) resolveCandidateGateIdentity(string, string) (string, string, error) {
	probe.closureCalls++
	return "", "", errors.New("injected candidate closure failure")
}

// loadImageCacheRuntime 记录任何不应发生的实时云物料读取。
func (probe *allHitDependencyProbe) loadImageCacheRuntime(remoteRunConfig, remoteci.BaselineState) (remoteImageCacheRuntime, error) {
	probe.runtimeCalls++
	return remoteImageCacheRuntime{}, errors.New("injected ImageCache runtime failure")
}

// bindMissExecution 记录任何不应发生的 MISS 绑定。
func (probe *allHitDependencyProbe) bindMissExecution(*remoteci.Coordinator, context.Context, *remoteci.PreparedRun, remoteci.MissExecutionBinding) error {
	probe.bindCalls++
	return errors.New("injected MISS binding failure")
}

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
		allReused: probe.allReused, resolveCandidateGateIdentity: probe.resolveCandidateGateIdentity,
		loadImageCacheRuntime: probe.loadImageCacheRuntime, bindMissExecution: probe.bindMissExecution,
		reloadPlanning: probe.reloadPlanning, runPrepared: probe.runPrepared, finalizeEvidence: probe.finalizeEvidence,
	}
}

func TestExecutePreparedRemoteRunAllHitSkipsMissDependencies(t *testing.T) {
	probe := &allHitDependencyProbe{t: t}
	result, _, err := executePreparedRemoteRun(
		context.Background(), remoteRunOptions{}, remoteRunConfig{}, remoteci.BaselineState{},
		"runner", remoteci.RunInput{}, nil, nil, probe.dependencies(),
	)
	assertAllHitExecution(t, result, err, probe)
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
	if probe.closureCalls != 0 || probe.runtimeCalls != 0 || probe.bindCalls != 0 {
		t.Fatalf("all-hit MISS dependency calls = closure:%d runtime:%d bind:%d, want all zero", probe.closureCalls, probe.runtimeCalls, probe.bindCalls)
	}
	if probe.reloadCalls != 1 || probe.runCalls != 1 || probe.finalizeCalls != 1 {
		t.Fatalf("all-hit terminal calls = reload:%d run:%d finalize:%d, want each once", probe.reloadCalls, probe.runCalls, probe.finalizeCalls)
	}
}

type partialMissDependencyProbe struct {
	t                                     *testing.T
	sourceDigest, toolchainDigest         string
	closureCalls, runtimeCalls, bindCalls int
	bound                                 remoteci.MissExecutionBinding
}

// allReused 固定返回存在 MISS。
func (*partialMissDependencyProbe) allReused(*remoteci.PreparedRun) bool { return false }

// resolveCandidateGateIdentity 返回 exact candidate Gate identity。
func (probe *partialMissDependencyProbe) resolveCandidateGateIdentity(repositoryRoot, tree string) (string, string, error) {
	probe.closureCalls++
	if repositoryRoot != "/repo" || tree != strings.Repeat("c", 40) {
		probe.t.Fatalf("candidate closure identity = %q %q", repositoryRoot, tree)
	}
	return probe.sourceDigest, probe.toolchainDigest, nil
}

// loadImageCacheRuntime 返回 MISS execution runtime。
func (probe *partialMissDependencyProbe) loadImageCacheRuntime(remoteRunConfig, remoteci.BaselineState) (remoteImageCacheRuntime, error) {
	probe.runtimeCalls++
	return remoteImageCacheRuntime{Image: "registry.example/runtime:miss", SnapshotID: "snap-miss", CacheOnly: true}, nil
}

// bindMissExecution 捕获严格 MISS 绑定。
func (probe *partialMissDependencyProbe) bindMissExecution(_ *remoteci.Coordinator, _ context.Context, _ *remoteci.PreparedRun, binding remoteci.MissExecutionBinding) error {
	probe.bindCalls++
	probe.bound = binding
	return nil
}

// dependencies 组装 partial MISS 依赖。
func (probe *partialMissDependencyProbe) dependencies() remotePreparedRunDependencies {
	return remotePreparedRunDependencies{
		allReused: probe.allReused, resolveCandidateGateIdentity: probe.resolveCandidateGateIdentity,
		loadImageCacheRuntime: probe.loadImageCacheRuntime, bindMissExecution: probe.bindMissExecution,
		reloadPlanning: noOpRemoteReloadPlanning, runPrepared: passedRemotePreparedRun,
		finalizeEvidence: noOpRemoteEvidenceFinalizer,
	}
}

// noOpRemoteReloadPlanning 跳过与本断言无关的校准重载。
func noOpRemoteReloadPlanning(remoteRunOptions, remoteci.BaselineState, string, remoteci.RunInput, *remoteci.PreparedRun) error {
	return nil
}

// passedRemotePreparedRun 返回测试用 PASS。
func passedRemotePreparedRun(*remoteci.Coordinator, context.Context, *remoteci.PreparedRun) (remoteci.RunResult, error) {
	return remoteci.RunResult{Status: gatecontract.ResultStatusPassed}, nil
}

// noOpRemoteEvidenceFinalizer 跳过与本断言无关的证据写入。
func noOpRemoteEvidenceFinalizer(remoteci.RunInput, *remoteci.RunResult, error) error { return nil }

func TestExecutePreparedRemoteRunPartialMissBindsDependenciesOnce(t *testing.T) {
	probe := &partialMissDependencyProbe{
		t: t, sourceDigest: "sha256:" + strings.Repeat("a", 64),
		toolchainDigest: "sha256:" + strings.Repeat("b", 64),
	}
	input := remoteci.RunInput{RepositoryRoot: "/repo", Tree: strings.Repeat("c", 40)}
	_, gotInput, err := executePreparedRemoteRun(
		context.Background(), remoteRunOptions{}, remoteRunConfig{}, remoteci.BaselineState{},
		"runner", input, nil, nil, probe.dependencies(),
	)
	assertPartialMissExecution(t, gotInput, err, probe)
}

// assertPartialMissExecution 验证 partial MISS 的闭包、runtime 与绑定都恰好一次。
func assertPartialMissExecution(t *testing.T, gotInput remoteci.RunInput, err error, probe *partialMissDependencyProbe) {
	t.Helper()
	if err != nil {
		t.Fatalf("executePreparedRemoteRun() error = %v", err)
	}
	assertPartialMissCalls(t, probe)
	assertPartialMissBinding(t, probe)
	assertPartialMissReturnedInput(t, gotInput, probe)
}

// assertPartialMissCalls 验证 MISS-only 依赖各调用一次。
func assertPartialMissCalls(t *testing.T, probe *partialMissDependencyProbe) {
	t.Helper()
	if probe.closureCalls != 1 || probe.runtimeCalls != 1 || probe.bindCalls != 1 {
		t.Fatalf("partial MISS dependency calls = closure:%d runtime:%d bind:%d, want each once", probe.closureCalls, probe.runtimeCalls, probe.bindCalls)
	}
}

// assertPartialMissBinding 验证绑定携带 exact candidate 与 execution identity。
func assertPartialMissBinding(t *testing.T, probe *partialMissDependencyProbe) {
	t.Helper()
	bound := probe.bound
	if bound.CandidateGateSourceSHA256 != probe.sourceDigest ||
		bound.CandidateGateToolchainSHA256 != probe.toolchainDigest ||
		bound.ExecutionImageCacheSnapshotID != "snap-miss" || !bound.ImageCacheOnly {
		t.Fatalf("bound MISS identity = %#v", bound)
	}
}

// assertPartialMissReturnedInput 验证返回输入与冻结绑定一致。
func assertPartialMissReturnedInput(t *testing.T, gotInput remoteci.RunInput, probe *partialMissDependencyProbe) {
	t.Helper()
	if gotInput.CandidateGateSourceSHA256 != probe.sourceDigest ||
		gotInput.ExecutionImageCacheSnapshotID != "snap-miss" {
		t.Fatalf("returned MISS input = %#v", gotInput)
	}
}
