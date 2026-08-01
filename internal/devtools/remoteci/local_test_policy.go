package remoteci

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// LocalLightTestMaxDurationMS 是允许占用本机资源的云端观测耗时上限。
const LocalLightTestMaxDurationMS int64 = 1_000

// LocalLightTestDecision 说明一个缓存未命中目标能否占用本机测试资源。
type LocalLightTestDecision struct {
	Eligible           bool
	Reason             string
	ObservedDurationMS int64
}

// DecideLocalLightTest 只放行具备可比快速云端证据的精确非 race Go 测试。
func DecideLocalLightTest(workload gate.Workload, input RunInput) (LocalLightTestDecision, error) {
	if input.ForceRerun {
		return localLightTestRejected("forced reruns must refresh remote proof"), nil
	}
	parent, testTarget, rejection, err := resolveLocalLightTestTarget(workload)
	if err != nil {
		return LocalLightTestDecision{}, err
	}
	if rejection != "" {
		return localLightTestRejected(rejection), nil
	}
	packageWorkload, err := gate.NewGoPackageWorkload(parent, testTarget.Package, 1)
	if err != nil {
		return LocalLightTestDecision{}, err
	}
	targetDuration, targetFound := comparableLocalTargetDuration(
		input,
		workload,
		packageWorkload,
		testTarget.Name,
	)
	if !targetFound {
		return localLightTestRejected("exact test has no comparable cloud PASS timing"), nil
	}
	totalDuration, totalFound := comparableLocalWorkloadDuration(input, workload, packageWorkload)
	if !totalFound {
		return localLightTestRejected("test workload has no comparable cloud total timing"), nil
	}
	observed := max(targetDuration, totalDuration)
	if observed > LocalLightTestMaxDurationMS {
		return localLightTestRejected(fmt.Sprintf(
			"cloud-observed duration %dms exceeds the %dms local ceiling",
			observed,
			LocalLightTestMaxDurationMS,
		)), nil
	}
	return LocalLightTestDecision{
		Eligible:           true,
		Reason:             "exact test is within the cloud-observed local budget",
		ObservedDurationMS: observed,
	}, nil
}

// resolveLocalLightTestTarget 解析并拒绝不能进入本机轻量通道的 workload 身份。
func resolveLocalLightTestTarget(workload gate.Workload) (
	gate.GateID,
	gate.GoTestTarget,
	string,
	error,
) {
	parent, kind, target, targeted, err := gate.ParseWorkloadID(workload.ID)
	if err != nil {
		return "", gate.GoTestTarget{}, "", err
	}
	if !targeted || kind != gate.WorkloadTargetGoTest {
		return "", gate.GoTestTarget{}, "only one exact Go test may run on the host", nil
	}
	if parent != gate.GateIDBackendTestWithGuard {
		return "", gate.GoTestTarget{}, "race and non-standard test workloads are remote-only", nil
	}
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		return "", gate.GoTestTarget{}, "", err
	}
	if !strings.HasPrefix(testTarget.Name, "Test") {
		return "", gate.GoTestTarget{}, "fuzz, example, and benchmark workloads are remote-only", nil
	}
	return parent, testTarget, "", nil
}

// comparableLocalTargetDuration 返回同环境下精确测试的最大成功耗时。
func comparableLocalTargetDuration(
	input RunInput,
	workload gate.Workload,
	packageWorkload gate.Workload,
	targetName string,
) (int64, bool) {
	var duration int64
	var found bool
	for _, sample := range input.LedgerSnapshot.Ledger.Samples {
		if !localTargetSampleMatches(sample, input, workload, packageWorkload, targetName) {
			continue
		}
		duration = max(duration, sample.DurationMS)
		found = true
	}
	return duration, found
}

// localTargetSampleMatches 校验测试级样本的结果、环境和父 workload 身份。
func localTargetSampleMatches(
	sample gate.DurationSample,
	input RunInput,
	workload gate.Workload,
	packageWorkload gate.Workload,
	targetName string,
) bool {
	if !sample.Succeeded || sample.TargetStatus != gate.GoTestStatusPass ||
		sample.TargetKind != gate.WorkloadKindGoTest || sample.TargetName != targetName ||
		!localSampleEnvironmentMatches(sample, input) {
		return false
	}
	return localWorkloadIdentityMatches(sample.ParentWorkloadID, sample.ParentCommandDigest, packageWorkload) ||
		localWorkloadIdentityMatches(sample.ParentWorkloadID, sample.ParentCommandDigest, workload)
}

// comparableLocalWorkloadDuration 返回同环境下原子或包 workload 的最大成功总耗时。
func comparableLocalWorkloadDuration(
	input RunInput,
	workload gate.Workload,
	packageWorkload gate.Workload,
) (int64, bool) {
	var duration int64
	var found bool
	for _, sample := range input.LedgerSnapshot.Ledger.Samples {
		if !sample.Succeeded || sample.TargetName != "" || !localSampleEnvironmentMatches(sample, input) {
			continue
		}
		if !localWorkloadIdentityMatches(sample.Bucket.WorkloadID, sample.Bucket.CommandDigest, workload) &&
			!localWorkloadIdentityMatches(sample.Bucket.WorkloadID, sample.Bucket.CommandDigest, packageWorkload) {
			continue
		}
		duration = max(duration, sample.DurationMS)
		found = true
	}
	return duration, found
}

func localWorkloadIdentityMatches(id string, digest string, workload gate.Workload) bool {
	return id == workload.ID && digest == workload.CommandDigest
}

func localSampleEnvironmentMatches(sample gate.DurationSample, input RunInput) bool {
	return sample.Bucket.Platform == input.Platform &&
		sample.Bucket.Runner == input.RunnerIdentityDigest &&
		sample.Bucket.Toolchain == input.ToolchainDigest
}

func localLightTestRejected(reason string) LocalLightTestDecision {
	return LocalLightTestDecision{Reason: reason}
}
