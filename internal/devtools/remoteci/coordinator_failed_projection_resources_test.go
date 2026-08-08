package remoteci

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRecordRemoteCIRunRejectsMalformedOptionalEvidence 确认失败运行的失真证据在唯一投影入口直接阻断。
func TestRecordRemoteCIRunRejectsMalformedOptionalEvidence(t *testing.T) {
	store := newPartialResultsLedgerStore(t)
	started := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	jobID := "job-malformed-optional-evidence"
	shardID := "sha256:" + strings.Repeat("e", 64)
	agentDigest := "sha256:" + strings.Repeat("d", 64)
	warning := failedProjectionWarning(jobID, shardID, agentDigest, started)
	result := RunResult{
		AcceptedGeneration:           1,
		JobID:                        jobID,
		AgentTokenDigest:             agentDigest,
		Entrypoint:                   gate.CIEntrypointGitPreCommit,
		Profile:                      gate.ProfileLocalFast,
		PlanDigest:                   "sha256:plan",
		SourceTreeSHA:                strings.Repeat("f", 40),
		CandidateGateSourceSHA256:    "sha256:" + strings.Repeat("b", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("c", 64),
		ImageCacheSnapshotID:         "snapshot-1",
		RunnerImage:                  "ubuntu:22.04",
		Status:                       gate.ResultStatusFailed,
		ExecutionMode:                gate.DurationExecutionModeCalibration,
		StartedAt:                    started,
		CompletedAt:                  started.Add(time.Second),
		Shards: []ShardResult{{
			ShardIdentity: shardID, ContainerGroup: "eci-created", ContainerStatus: "Failed",
			ResourceClass: "calibration", Resources: eci.Resources{CPU: 6, MemoryGiB: 0},
			ExecutedWorkloads:     []gate.GateID{"guard:failed"},
			MaterializationTiming: gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: shardID},
			Report:                gate.PlanExecutionReport{CompileGroupExecutions: []gate.CompileGroupExecution{{GroupID: "invalid"}}},
		}},
		OptimizationWarnings: append([]string{warning.WarningText}, compileGroupPlanWarnings([]gate.CompileGroup{{
			GroupID: "sha256:" + strings.Repeat("c", 64), PackageTarget: "./internal/archtest", ResourceClassID: "calibration",
			BatchPlanWarning: "critical_batch_plus_compile_ms=104000 exceeds_target_ms=100000 at_max_batches=4",
		}})...),
		TimingWarnings: []gate.RemoteCITimingWarning{warning},
		GateExecutions: []gate.PlanGateExecution{{GateID: "guard:invalid-child"}},
	}
	catalog := gate.WorkloadCatalog{Version: 1, Authoritative: true, Workloads: []gate.Workload{{
		ID: "guard:failed", Kind: gate.WorkloadKindGuard, CommandDigest: strings.Repeat("1", 64), BootstrapEstimateMS: 1_000, Shardable: true,
	}}}
	recordPartialResultsCatalog(t, store, &result, catalog, started)
	if _, inserted, err := store.RecordLiveRemoteCITimingWarning(warning); err != nil || !inserted {
		t.Fatalf("record live warning inserted=%t error=%v", inserted, err)
	}
	runErr := errors.Join(
		errors.New("compile group execution count does not match manifest"),
		errors.New("remote CI calibration duration resource receipt drifted"),
		errors.New("remote CI shard resources are invalid"),
	)
	if err := recordRemoteCIRun(store, result, runErr); err == nil {
		t.Fatal("recordRemoteCIRun() accepted malformed optional evidence")
	}
	if _, err := store.LoadRemoteCIRun(jobID); err == nil {
		t.Fatal("malformed optional evidence run was persisted after fail-fast rejection")
	}
}

func failedProjectionWarning(jobID, shardID, agentDigest string, started time.Time) gate.RemoteCITimingWarning {
	warning := gate.RemoteCITimingWarning{
		JobID: jobID, AgentTokenDigest: agentDigest, AcceptedGeneration: 1,
		Scope: cicontract.TimingScopeShard, ShardIdentity: shardID,
		EvidenceKind: cicontract.TimingWarningEvidenceRunning, Action: cicontract.TimingWarningWarnAndContinue,
		EvidenceStartedAt: started, ObservedAt: started.Add(cicontract.ShardTargetDuration),
		EvidenceDurationMS: cicontract.ShardTargetDuration.Milliseconds(), TargetMS: cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = gate.CanonicalRemoteCITimingWarningText(warning)
	return warning
}
