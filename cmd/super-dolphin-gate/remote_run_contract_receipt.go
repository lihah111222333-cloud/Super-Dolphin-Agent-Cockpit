package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// validateRemoteRunContract 校验远程运行与输入、权威账本及全部必需检查回执的同一性。
func validateRemoteRunContract(
	input remoteci.RunInput,
	acceptedGeneration uint64,
	result remoteci.RunResult,
) ([]cicontract.CheckObservation, []gatecontract.CheckReceiptRecord, error) {
	if err := validateRemoteRunInvocationIdentity(input, acceptedGeneration, result); err != nil {
		return nil, nil, err
	}
	plan, catalog, err := remoteRunContractPlanAndCatalog(input, result)
	if err != nil {
		return nil, nil, err
	}
	observations, err := remoteRunCheckObservations(plan, catalog, input.ImageCacheSnapshotID, result)
	if err != nil {
		return nil, nil, err
	}
	requiredChecks, err := gatecontract.RequiredChecksForWorkloadCatalog(catalog)
	if err != nil {
		return nil, nil, err
	}
	if err := cicontract.ValidateRequiredChecksObservedPassFor(requiredChecks, observations); err != nil {
		return nil, nil, err
	}
	if !remoteRunIsFullAuthoritativeAcceptance(catalog, result) {
		return nil, nil, errors.New("remote CI run is not a full authoritative acceptance")
	}
	if err := validateRemoteRunRecordedAuthority(input, acceptedGeneration, result); err != nil {
		return nil, nil, err
	}
	receipts, err := remoteRunCheckReceipts(result, acceptedGeneration, observations)
	if err != nil {
		return nil, nil, err
	}
	return observations, receipts, nil
}

// validateRemoteRunInvocationIdentity 校验本次输入、结果和 accepted generation 的不可变绑定。
func validateRemoteRunInvocationIdentity(input remoteci.RunInput, acceptedGeneration uint64, result remoteci.RunResult) error {
	if acceptedGeneration == 0 || input.AcceptedGeneration != acceptedGeneration || result.AcceptedGeneration != acceptedGeneration {
		return errors.New("remote CI run accepted generation is not bound to its input")
	}
	if err := cicontract.ValidateAgentTokenDigest(input.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote CI input agent token digest: %w", err)
	}
	if result.AgentTokenDigest != input.AgentTokenDigest {
		return errors.New("remote CI result agent token digest does not match input")
	}
	if result.Force != input.Force {
		return errors.New("remote CI result force mode does not match input")
	}
	return nil
}

// validateRemoteRunRecordedAuthority 只接受本次调用精确回读到的 SQLite 权威账本记录。
func validateRemoteRunRecordedAuthority(input remoteci.RunInput, acceptedGeneration uint64, result remoteci.RunResult) error {
	if input.LedgerStore == nil {
		return errors.New("remote CI timing authority store is required")
	}
	recorded, err := input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		return fmt.Errorf("read back remote CI timing authority: %w", err)
	}
	if !remoteRunRecordedIdentityMatches(recorded, input, acceptedGeneration, result) {
		return errors.New("recorded remote CI run identity does not exactly match this invocation")
	}
	if err := validateRemoteRunStoredWorkloadResults(recorded, result); err != nil {
		return err
	}
	return validateRemoteRunFreshTimingAuthority(recorded, result)
}

// remoteRunRecordedIdentityMatches 比对持久记录与当前候选、计划、token 和状态的精确身份。
func remoteRunRecordedIdentityMatches(
	recorded gatecontract.RemoteCIRunRecord,
	input remoteci.RunInput,
	acceptedGeneration uint64,
	result remoteci.RunResult,
) bool {
	return remoteRunRecordedInvocationBindingMatches(recorded, input, acceptedGeneration, result) &&
		remoteRunRecordedResultBindingMatches(recorded, result)
}

// remoteRunRecordedInvocationBindingMatches 比对回执与本次运行的代际、快照、身份和强制模式。
func remoteRunRecordedInvocationBindingMatches(
	recorded gatecontract.RemoteCIRunRecord,
	input remoteci.RunInput,
	acceptedGeneration uint64,
	result remoteci.RunResult,
) bool {
	return recorded.AcceptedGeneration == acceptedGeneration &&
		recorded.ImageCacheSnapshotID == input.ImageCacheSnapshotID &&
		recorded.ImageCacheSnapshotID == result.ImageCacheSnapshotID &&
		recorded.AgentTokenDigest == input.AgentTokenDigest &&
		recorded.Force == input.Force && recorded.Force == result.Force
}

// remoteRunRecordedResultBindingMatches 比对回执与本次候选的源码、计划和状态。
func remoteRunRecordedResultBindingMatches(
	recorded gatecontract.RemoteCIRunRecord,
	result remoteci.RunResult,
) bool {
	return recorded.SourceTreeSHA == result.SourceTreeSHA &&
		recorded.PlanDigest == result.PlanDigest &&
		recorded.CatalogDigest == result.CatalogDigest &&
		recorded.Profile == result.Profile &&
		recorded.Status == result.Status &&
		recorded.Authoritative == result.Authoritative
}

// validateRemoteRunFreshTimingAuthority 要求 fresh 运行有完整时序，reuse-only 运行没有伪造时序。
func validateRemoteRunFreshTimingAuthority(recorded gatecontract.RemoteCIRunRecord, result remoteci.RunResult) error {
	if len(result.FreshWorkloadExecutions) != 0 {
		if err := gatecontract.ValidateAuthoritativeTimingObservations(recorded.JobID, recorded.TimingObservations, recorded.WorkloadExecutions, recorded.Shards); err != nil {
			return fmt.Errorf("authoritative fresh timing observations: %w", err)
		}
		return nil
	}
	if len(recorded.WorkloadExecutions) != 0 || len(recorded.Shards) != 0 || len(recorded.TimingObservations) != 0 {
		return errors.New("reuse-only remote CI run must not persist fresh timing authority")
	}
	return nil
}

// validateRemoteRunStoredWorkloadResults 校验账本回读的 fresh/reused 工作负载结果与当前回执相同。
func validateRemoteRunStoredWorkloadResults(recorded gatecontract.RemoteCIRunRecord, result remoteci.RunResult) error {
	fresh := remoteRunFreshExecutionMap(result.FreshWorkloadExecutions)
	if err := validateRemoteRunStoredFreshExecutions(recorded.WorkloadExecutions, fresh); err != nil {
		return err
	}
	want, err := remoteRunExpectedWorkloadResults(result, fresh)
	if err != nil {
		return err
	}
	return validateRemoteRunStoredWorkloadResultSet(recorded.WorkloadResults, want)
}

// remoteRunFreshExecutionMap 按工作负载标识汇总当前结果中的 fresh 执行回执。
func remoteRunFreshExecutionMap(executions []gatecontract.PlanGateExecution) map[gatecontract.GateID]gatecontract.PlanGateExecution {
	fresh := make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(executions))
	for _, execution := range executions {
		fresh[execution.GateID] = execution
	}
	return fresh
}

// validateRemoteRunStoredFreshExecutions 校验账本中的 fresh 执行记录逐项等同于当前结果。
func validateRemoteRunStoredFreshExecutions(
	recorded []gatecontract.PlanGateExecution,
	fresh map[gatecontract.GateID]gatecontract.PlanGateExecution,
) error {
	if len(recorded) != len(fresh) {
		return errors.New("recorded remote CI fresh workload execution count does not match result")
	}
	for _, execution := range recorded {
		if expected, found := fresh[execution.GateID]; !found || !remoteRunStoredFreshExecutionMatches(execution, expected) {
			return fmt.Errorf("recorded remote CI fresh workload %q does not exactly match result", execution.GateID)
		}
	}
	return nil
}

// validateRemoteRunStoredAggregateExecutions 校验 SQLite gate 聚合投影与当前结果逐项一致。
func validateRemoteRunStoredAggregateExecutions(
	recorded []gatecontract.PlanGateExecution,
	expected []gatecontract.PlanGateExecution,
) error {
	if len(recorded) != len(expected) {
		return errors.New("recorded remote CI aggregate execution count does not match result")
	}
	recordedByID := make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(recorded))
	for _, execution := range recorded {
		if _, duplicate := recordedByID[execution.GateID]; duplicate {
			return fmt.Errorf("recorded remote CI aggregate execution %q is duplicated", execution.GateID)
		}
		recordedByID[execution.GateID] = execution
	}
	expectedByID := make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(expected))
	for _, execution := range expected {
		if _, duplicate := expectedByID[execution.GateID]; duplicate {
			return fmt.Errorf("remote CI aggregate execution %q is duplicated in result", execution.GateID)
		}
		expectedByID[execution.GateID] = execution
	}
	for gateID, expectedExecution := range expectedByID {
		recordedExecution, found := recordedByID[gateID]
		if !found || !remoteRunStoredExecutionProjectionMatches(recordedExecution, expectedExecution) {
			return fmt.Errorf("recorded remote CI aggregate execution %q does not exactly match result", gateID)
		}
	}
	return nil
}

// validateRemoteRunStoredAggregateExecutionReadback 在 finalizer 提升 authority 前重载并校验 gate 聚合投影。
func validateRemoteRunStoredAggregateExecutionReadback(input remoteci.RunInput, result remoteci.RunResult) error {
	if input.LedgerStore == nil {
		return errors.New("remote CI final authority store is required")
	}
	recorded, err := input.LedgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		return fmt.Errorf("read back remote CI aggregate executions before finalization: %w", err)
	}
	return validateRemoteRunStoredAggregateExecutions(recorded.Executions, result.GateExecutions)
}

// remoteRunStoredFreshExecutionMatches 比较 SQLite 实际持久化的执行投影。
// 原始有界日志不进入增长账本；其内容只通过同一回执中的 LogDigest 绑定。
func remoteRunStoredFreshExecutionMatches(recorded, expected gatecontract.PlanGateExecution) bool {
	return remoteRunStoredExecutionProjectionMatches(recorded, expected)
}

// remoteRunStoredExecutionProjectionMatches 比较 SQLite 执行投影中实际持久化的字段。
func remoteRunStoredExecutionProjectionMatches(recorded, expected gatecontract.PlanGateExecution) bool {
	if len(recorded.Log) != 0 {
		return false
	}
	recorded.Log = nil
	expected.Log = nil
	if len(recorded.TestTimings) == 0 {
		recorded.TestTimings = nil
	}
	if len(expected.TestTimings) == 0 {
		expected.TestTimings = nil
	}
	return reflect.DeepEqual(recorded, expected)
}

// remoteRunExpectedWorkloadResults 生成本次 fresh/reused 组合应持久化的完整工作负载结果。
func remoteRunExpectedWorkloadResults(
	result remoteci.RunResult,
	fresh map[gatecontract.GateID]gatecontract.PlanGateExecution,
) (map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, error) {
	identities := make(map[gatecontract.GateID]gatecontract.WorkloadPassIdentity, len(result.WorkloadPassIdentities))
	for _, identity := range result.WorkloadPassIdentities {
		identities[identity.WorkloadID] = identity
	}
	want := make(map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult, len(result.ReusedWorkloads)+len(fresh))
	for _, evidence := range result.ReusedWorkloads {
		want[evidence.Identity.WorkloadID] = gatecontract.RemoteCIWorkloadResult{Identity: evidence.Identity, Disposition: gatecontract.WorkloadDispositionReused, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration, EvidenceSHA256: evidence.EvidenceSHA256}
	}
	for workloadID := range fresh {
		identity, found := identities[workloadID]
		if !found {
			return nil, fmt.Errorf("fresh remote CI workload %q lacks identity", workloadID)
		}
		want[workloadID] = gatecontract.RemoteCIWorkloadResult{Identity: identity, Disposition: gatecontract.WorkloadDispositionExecuted, OriginJobID: result.JobID, OriginAcceptedGeneration: result.AcceptedGeneration}
	}
	return want, nil
}

// validateRemoteRunStoredWorkloadResultSet 校验账本的 workload result 集合无缺失且逐项一致。
func validateRemoteRunStoredWorkloadResultSet(
	recorded []gatecontract.RemoteCIWorkloadResult,
	want map[gatecontract.GateID]gatecontract.RemoteCIWorkloadResult,
) error {
	if len(recorded) != len(want) {
		return errors.New("recorded remote CI workload result count does not match result")
	}
	for _, stored := range recorded {
		if expected, found := want[stored.Identity.WorkloadID]; !found || stored != expected {
			return fmt.Errorf("recorded remote CI workload result %q does not exactly match result", stored.Identity.WorkloadID)
		}
	}
	return nil
}

// remoteRunContractPlanAndCatalog 从 coordinator 已准备并执行的 SQLite 权威目录加载候选目录。
func remoteRunContractPlanAndCatalog(input remoteci.RunInput, result remoteci.RunResult) (gatecontract.GatePlan, gatecontract.WorkloadCatalog, error) {
	plan, err := gatecontract.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, fmt.Errorf("build remote CI gate plan: %w", err)
	}
	if input.LedgerStore == nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, errors.New("remote CI workload catalog authority store is required")
	}
	record, err := input.LedgerStore.LoadWorkloadCatalogRecord(result.CatalogDigest)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, fmt.Errorf("load remote CI workload catalog authority: %w", err)
	}
	if record.CatalogDigest != result.CatalogDigest {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, errors.New("stored remote CI workload catalog digest does not match result")
	}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(record.Catalog)
	if err != nil {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, fmt.Errorf("digest stored remote CI workload catalog: %w", err)
	}
	if catalogDigest != result.CatalogDigest {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, errors.New("stored remote CI workload catalog content does not match result")
	}
	if !remoteRunCatalogObservationMatches(record.Observations, result) {
		return gatecontract.GatePlan{}, gatecontract.WorkloadCatalog{}, errors.New("stored remote CI workload catalog has no matching observation")
	}
	return plan, record.Catalog, nil
}

// remoteRunCatalogObservationMatches 确认目录观测与本次结果的源码、入口、profile 和代际完全一致。
func remoteRunCatalogObservationMatches(observations []gatecontract.WorkloadCatalogObservation, result remoteci.RunResult) bool {
	for _, observation := range observations {
		if observation.SourceTreeSHA == result.SourceTreeSHA &&
			observation.Entrypoint == result.Entrypoint &&
			observation.Profile == result.Profile &&
			observation.AcceptedGeneration == result.AcceptedGeneration {
			return true
		}
	}
	return false
}

// remoteRunObservationCoverage 聚合计划工作负载及其本次 fresh/reused 覆盖。
type remoteRunObservationCoverage struct {
	expected map[gatecontract.GateID]gatecontract.Workload
	fresh    map[gatecontract.GateID]gatecontract.PlanGateExecution
	reused   map[gatecontract.GateID]gatecontract.WorkloadPassEvidence
	checks   map[cicontract.RequiredCheck]cicontract.CheckObservation
}

// remoteRunCheckObservations 仅依据计划工作负载及当前运行回执生成必需检查观测。
func remoteRunCheckObservations(
	plan gatecontract.GatePlan,
	catalog gatecontract.WorkloadCatalog,
	acceptedSnapshotID string,
	result remoteci.RunResult,
) ([]cicontract.CheckObservation, error) {
	if err := validateRemoteRunObservationInput(plan, catalog, acceptedSnapshotID, result); err != nil {
		return nil, err
	}
	coverage, err := newRemoteRunObservationCoverage(catalog, acceptedSnapshotID, result)
	if err != nil {
		return nil, err
	}
	if err := coverRemoteRunFreshObservations(&coverage, result.FreshWorkloadExecutions); err != nil {
		return nil, err
	}
	if err := coverRemoteRunReusedObservations(&coverage, result.ReusedWorkloads); err != nil {
		return nil, err
	}
	if err := validateRemoteRunWorkloadCoverage(coverage); err != nil {
		return nil, err
	}
	return finalizeRemoteRunCheckObservations(coverage, catalog, result)
}

// validateRemoteRunObservationInput 校验计划、目录、快照及结果属于同一候选运行。
func validateRemoteRunObservationInput(
	plan gatecontract.GatePlan,
	catalog gatecontract.WorkloadCatalog,
	acceptedSnapshotID string,
	result remoteci.RunResult,
) error {
	if err := validateRemoteRunPlanAndCatalog(plan, catalog); err != nil {
		return err
	}
	if err := validateRemoteRunResultPlanBinding(plan, catalog, result); err != nil {
		return err
	}
	return validateRemoteRunSnapshotBinding(acceptedSnapshotID, result)
}

// validateRemoteRunPlanAndCatalog 校验候选计划及其工作负载目录自身有效。
func validateRemoteRunPlanAndCatalog(plan gatecontract.GatePlan, catalog gatecontract.WorkloadCatalog) error {
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("validate remote CI gate plan: %w", err)
	}
	if err := gatecontract.ValidateWorkloadCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI workload catalog: %w", err)
	}
	return nil
}

// validateRemoteRunResultPlanBinding 校验结果精确绑定到本次候选计划、目录和源码树。
func validateRemoteRunResultPlanBinding(
	plan gatecontract.GatePlan,
	catalog gatecontract.WorkloadCatalog,
	result remoteci.RunResult,
) error {
	if result.Profile != plan.Profile || result.PlanDigest != plan.PlanDigest {
		return errors.New("remote CI result does not bind the executed gate plan")
	}
	catalogDigest, err := gatecontract.WorkloadCatalogDigest(catalog)
	if err != nil {
		return fmt.Errorf("digest remote CI workload catalog: %w", err)
	}
	if result.CatalogDigest != catalogDigest {
		return errors.New("remote CI result does not bind the executed workload catalog")
	}
	if result.SourceTreeSHA != plan.Source.SourceTreeSHA {
		return errors.New("remote CI result source tree does not match the executed gate plan")
	}
	if result.Status != gatecontract.ResultStatusPassed {
		return fmt.Errorf("remote CI run status %q cannot accept executed checks", result.Status)
	}
	return nil
}

// validateRemoteRunSnapshotBinding 校验结果使用本次已接受的唯一镜像快照。
func validateRemoteRunSnapshotBinding(acceptedSnapshotID string, result remoteci.RunResult) error {
	if acceptedSnapshotID == "" {
		return errors.New("remote CI accepted snapshot is missing")
	}
	if result.ImageCacheSnapshotID != acceptedSnapshotID {
		return errors.New("remote CI result image cache snapshot does not match the accepted run snapshot")
	}
	return nil
}

// newRemoteRunObservationCoverage 为每个计划工作负载初始化本次运行的检查观测。
func newRemoteRunObservationCoverage(
	catalog gatecontract.WorkloadCatalog,
	acceptedSnapshotID string,
	result remoteci.RunResult,
) (remoteRunObservationCoverage, error) {
	coverage := remoteRunObservationCoverage{
		expected: make(map[gatecontract.GateID]gatecontract.Workload, len(catalog.Workloads)),
		fresh:    make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(result.FreshWorkloadExecutions)),
		reused:   make(map[gatecontract.GateID]gatecontract.WorkloadPassEvidence, len(result.ReusedWorkloads)),
		checks:   make(map[cicontract.RequiredCheck]cicontract.CheckObservation, len(cicontract.RequiredChecks())),
	}
	for _, workload := range catalog.Workloads {
		check, err := remoteRunCheckForWorkload(workload.ID)
		if err != nil {
			return remoteRunObservationCoverage{}, fmt.Errorf("parse planned remote CI workload %q: %w", workload.ID, err)
		}
		coverage.checks[check] = cicontract.CheckObservation{
			Check: check, SourceTree: result.SourceTreeSHA, AcceptedSnapshotID: acceptedSnapshotID, PlanDigest: result.PlanDigest,
		}
		if workload.Shardable {
			coverage.expected[gatecontract.GateID(workload.ID)] = workload
		}
	}
	if err := coverRemoteRunOwnerObservations(&coverage, catalog, result.GateExecutions); err != nil {
		return remoteRunObservationCoverage{}, err
	}
	return coverage, nil
}

// coverRemoteRunOwnerObservations 只接受 coordinator 生成并绑定 canonical 前序结果的 owner-only release 证明。
func coverRemoteRunOwnerObservations(
	coverage *remoteRunObservationCoverage,
	catalog gatecontract.WorkloadCatalog,
	executions []gatecontract.PlanGateExecution,
) error {
	byID, ownerObserved, err := indexRemoteRunOwnerExecutions(executions)
	if err != nil {
		return err
	}
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			continue
		}
		if err := coverRemoteRunOwnerObservation(coverage, workload, byID, ownerObserved); err != nil {
			return err
		}
	}
	return nil
}

func indexRemoteRunOwnerExecutions(executions []gatecontract.PlanGateExecution) (map[gatecontract.GateID]gatecontract.PlanGateExecution, map[gatecontract.GateID]gatecontract.PlanGateExecution, error) {
	byID := make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(executions))
	ownerObserved := make(map[gatecontract.GateID]gatecontract.PlanGateExecution, len(executions))
	for _, execution := range executions {
		if _, duplicate := byID[execution.GateID]; duplicate {
			return nil, nil, fmt.Errorf("remote CI owner gate %q is duplicated", execution.GateID)
		}
		byID[execution.GateID] = execution
		if execution.GateID != gatecontract.GateIDReleaseLayeredCheck {
			ownerObserved[execution.GateID] = execution
		}
	}
	return byID, ownerObserved, nil
}

// coverRemoteRunOwnerObservation 校验单个 owner-only 工作负载的 coordinator 证明并生成检查观测。
func coverRemoteRunOwnerObservation(coverage *remoteRunObservationCoverage, workload gatecontract.Workload, byID, ownerObserved map[gatecontract.GateID]gatecontract.PlanGateExecution) error {
	execution, found := byID[gatecontract.GateID(workload.ID)]
	if !found {
		return fmt.Errorf("remote CI owner-only workload %q has no coordinator attestation", workload.ID)
	}
	if workload.ID != string(gatecontract.GateIDReleaseLayeredCheck) {
		return fmt.Errorf("remote CI unsupported owner-only workload %q", workload.ID)
	}
	if err := gatecontract.ValidateReleaseLayerAttestation(gatecontract.ProfileRelease, coverage.checks[cicontract.RequiredCheckGate].PlanDigest, ownerObserved, execution); err != nil {
		return fmt.Errorf("validate remote CI release owner attestation: %w", err)
	}
	if execution.StartedAt.IsZero() || execution.CompletedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) {
		return fmt.Errorf("remote CI owner-only workload %q has no positive execution time", workload.ID)
	}
	check, err := remoteRunCheckForWorkload(workload.ID)
	if err != nil {
		return fmt.Errorf("parse owner-only remote CI workload %q: %w", workload.ID, err)
	}
	observation := coverage.checks[check]
	observation.Executed, observation.Passed = true, true
	mergeRemoteRunObservationInterval(&observation, execution.StartedAt.UTC().Truncate(time.Millisecond), execution.CompletedAt.UTC().Truncate(time.Millisecond))
	coverage.checks[check] = observation
	return nil
}

// coverRemoteRunFreshObservations 以当前 job 的实际执行区间覆盖 fresh 工作负载。
func coverRemoteRunFreshObservations(coverage *remoteRunObservationCoverage, executions []gatecontract.PlanGateExecution) error {
	for _, execution := range executions {
		if err := coverRemoteRunFreshObservation(coverage, execution); err != nil {
			return err
		}
	}
	return nil
}

// coverRemoteRunFreshObservation 校验单个 fresh 回执并聚合其当前 job 区间。
func coverRemoteRunFreshObservation(coverage *remoteRunObservationCoverage, execution gatecontract.PlanGateExecution) error {
	if _, planned := coverage.expected[execution.GateID]; !planned {
		return fmt.Errorf("remote CI observed unplanned or duplicate workload %q", execution.GateID)
	}
	if _, duplicate := coverage.fresh[execution.GateID]; duplicate {
		return fmt.Errorf("remote CI fresh workload %q is duplicated", execution.GateID)
	}
	startedAt, completedAt, err := remoteRunFreshExecutionInterval(execution)
	if err != nil {
		return err
	}
	check, err := remoteRunCheckForWorkload(string(execution.GateID))
	if err != nil {
		return fmt.Errorf("parse executed remote CI workload %q: %w", execution.GateID, err)
	}
	observation := coverage.checks[check]
	mergeRemoteRunObservationInterval(&observation, startedAt, completedAt)
	observation.Executed, observation.Passed = true, true
	coverage.checks[check], coverage.fresh[execution.GateID] = observation, execution
	return nil
}

// remoteRunFreshExecutionInterval 返回并毫秒截断 fresh 回执的真实执行区间。
func remoteRunFreshExecutionInterval(execution gatecontract.PlanGateExecution) (time.Time, time.Time, error) {
	if execution.Status != gatecontract.ResultStatusPassed || execution.ExitCode != 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q did not execute and pass", execution.GateID)
	}
	if execution.StartedAt.IsZero() || execution.CompletedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) {
		return time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q has no positive actual execution time", execution.GateID)
	}
	startedAt := execution.StartedAt.UTC().Truncate(time.Millisecond)
	completedAt := execution.CompletedAt.UTC().Truncate(time.Millisecond)
	if !completedAt.After(startedAt) {
		return time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q has no positive millisecond execution time", execution.GateID)
	}
	return startedAt, completedAt, nil
}

// mergeRemoteRunObservationInterval 对同一检查的 fresh 工作负载区间取当前 job 的包络。
func mergeRemoteRunObservationInterval(observation *cicontract.CheckObservation, startedAt, completedAt time.Time) {
	if observation.StartedAtUnixMS == 0 || startedAt.UnixMilli() < observation.StartedAtUnixMS {
		observation.StartedAtUnixMS = startedAt.UnixMilli()
	}
	if observation.CompletedAtUnixMS == 0 || completedAt.UnixMilli() > observation.CompletedAtUnixMS {
		observation.CompletedAtUnixMS = completedAt.UnixMilli()
	}
}

// coverRemoteRunReusedObservations 以 canonical reuse proof 覆盖未在本次执行的工作负载。
func coverRemoteRunReusedObservations(coverage *remoteRunObservationCoverage, evidenceList []gatecontract.WorkloadPassEvidence) error {
	for _, evidence := range evidenceList {
		if err := coverRemoteRunReusedObservation(coverage, evidence); err != nil {
			return err
		}
	}
	return nil
}

// coverRemoteRunReusedObservation 校验单个复用证据且禁止它与 fresh 回执重叠。
func coverRemoteRunReusedObservation(coverage *remoteRunObservationCoverage, evidence gatecontract.WorkloadPassEvidence) error {
	workloadID := evidence.Identity.WorkloadID
	if _, planned := coverage.expected[workloadID]; !planned {
		return fmt.Errorf("remote CI reused unplanned workload %q", workloadID)
	}
	if _, duplicate := coverage.reused[workloadID]; duplicate {
		return fmt.Errorf("remote CI reused workload %q is duplicated", workloadID)
	}
	if _, alsoFresh := coverage.fresh[workloadID]; alsoFresh {
		return fmt.Errorf("remote CI workload %q cannot be both fresh and reused", workloadID)
	}
	if err := evidence.Validate(); err != nil {
		return fmt.Errorf("validate remote CI reused workload %q: %w", workloadID, err)
	}
	check, err := remoteRunCheckForWorkload(string(workloadID))
	if err != nil {
		return fmt.Errorf("parse reused remote CI workload %q: %w", workloadID, err)
	}
	observation := coverage.checks[check]
	observation.Reused, observation.Passed = true, true
	coverage.checks[check], coverage.reused[workloadID] = observation, evidence
	return nil
}

// validateRemoteRunWorkloadCoverage 要求每个计划工作负载恰有 fresh 或 reuse PASS 覆盖。
func validateRemoteRunWorkloadCoverage(coverage remoteRunObservationCoverage) error {
	for workloadID := range coverage.expected {
		if _, fresh := coverage.fresh[workloadID]; fresh {
			continue
		}
		if _, reused := coverage.reused[workloadID]; !reused {
			return fmt.Errorf("remote CI planned workload %q has neither current fresh PASS nor reuse evidence", workloadID)
		}
	}
	return nil
}

// finalizeRemoteRunCheckObservations 为全部必需检查写入 proof、当前 job 区间和 receipt 摘要。
func finalizeRemoteRunCheckObservations(
	coverage remoteRunObservationCoverage,
	catalog gatecontract.WorkloadCatalog,
	result remoteci.RunResult,
) ([]cicontract.CheckObservation, error) {
	observations := make([]cicontract.CheckObservation, 0, len(coverage.checks))
	for _, check := range cicontract.RequiredChecks() {
		observation, planned := coverage.checks[check]
		if !planned {
			continue
		}
		observation, err := finalizeRemoteRunCheckObservation(check, observation, catalog, coverage.reused, result)
		if err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

// finalizeRemoteRunCheckObservation 保持 fresh 时间在当前 job，reuse 只引用 proof 和当前 receipt 区间。
func finalizeRemoteRunCheckObservation(
	check cicontract.RequiredCheck,
	observation cicontract.CheckObservation,
	catalog gatecontract.WorkloadCatalog,
	reused map[gatecontract.GateID]gatecontract.WorkloadPassEvidence,
	result remoteci.RunResult,
) (cicontract.CheckObservation, error) {
	if !observation.Passed || (!observation.Executed && !observation.Reused) {
		return cicontract.CheckObservation{}, fmt.Errorf("remote CI required check %q has no PASS coverage", check)
	}
	if observation.Reused {
		proof, err := remoteRunReuseProofSHA256(check, catalog, reused)
		if err != nil {
			return cicontract.CheckObservation{}, err
		}
		observation.ReuseProofSHA256 = proof
	}
	if err := bindRemoteRunObservationCurrentInterval(&observation, check, result); err != nil {
		return cicontract.CheckObservation{}, err
	}
	observation.DurationMS = observation.CompletedAtUnixMS - observation.StartedAtUnixMS
	digest, err := cicontract.CheckObservationReceiptDigest(observation)
	if err != nil {
		return cicontract.CheckObservation{}, fmt.Errorf("hash remote CI required check %q observation: %w", check, err)
	}
	observation.ReceiptSHA256 = digest
	return observation, nil
}

// bindRemoteRunObservationCurrentInterval 保留 fresh 区间，reuse-only 改用本次 job 的当前回执区间。
func bindRemoteRunObservationCurrentInterval(observation *cicontract.CheckObservation, check cicontract.RequiredCheck, result remoteci.RunResult) error {
	if observation.Executed {
		if observation.StartedAtUnixMS <= 0 || observation.CompletedAtUnixMS <= observation.StartedAtUnixMS {
			return fmt.Errorf("remote CI required check %q has no current fresh execution timing", check)
		}
		return nil
	}
	startedAt, completedAt, err := remoteRunCurrentReceiptInterval(result)
	if err != nil {
		return fmt.Errorf("remote CI reused check %q has no current receipt interval: %w", check, err)
	}
	observation.StartedAtUnixMS, observation.CompletedAtUnixMS = startedAt.UnixMilli(), completedAt.UnixMilli()
	return nil
}

// remoteRunCurrentReceiptInterval 只返回本次 coordinator job 的回执区间，绝不采用 origin timing。
func remoteRunCurrentReceiptInterval(result remoteci.RunResult) (time.Time, time.Time, error) {
	startedAt := result.StartedAt.UTC().Truncate(time.Millisecond)
	completedAt := result.CompletedAt.UTC().Truncate(time.Millisecond)
	if startedAt.IsZero() || !completedAt.After(startedAt) {
		return time.Time{}, time.Time{}, errors.New("remote CI result has no positive current receipt interval")
	}
	return startedAt, completedAt, nil
}

// remoteRunReuseProofSHA256 对同一必需检查的复用证据摘要做 canonical SHA-256 绑定。
func remoteRunReuseProofSHA256(
	check cicontract.RequiredCheck,
	catalog gatecontract.WorkloadCatalog,
	reused map[gatecontract.GateID]gatecontract.WorkloadPassEvidence,
) (string, error) {
	digests, err := remoteRunReuseProofDigestSummary(check, catalog, reused)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(digests)
	if err != nil {
		return "", fmt.Errorf("encode remote CI reuse proof %q: %w", check, err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(payload)), nil
}

// remoteRunReuseProofDigestSummary 按目录顺序汇总某必需检查实际复用的 proof digest。
func remoteRunReuseProofDigestSummary(
	check cicontract.RequiredCheck,
	catalog gatecontract.WorkloadCatalog,
	reused map[gatecontract.GateID]gatecontract.WorkloadPassEvidence,
) ([]string, error) {
	digests := make([]string, 0)
	for _, workload := range catalog.Workloads {
		mapped, err := remoteRunCheckForWorkload(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("parse reused remote CI workload %q: %w", workload.ID, err)
		}
		if mapped != check {
			continue
		}
		if evidence, found := reused[gatecontract.GateID(workload.ID)]; found {
			digests = append(digests, evidence.EvidenceSHA256)
		}
	}
	if len(digests) == 0 {
		return nil, fmt.Errorf("remote CI required check %q has reuse without evidence", check)
	}
	return digests, nil
}

// remoteRunCheckForWorkload 将规范 workload 标识映射为唯一的必需检查分类。
func remoteRunCheckForWorkload(workloadID string) (cicontract.RequiredCheck, error) {
	return gatecontract.RequiredCheckForWorkloadID(workloadID)
}

// remoteRunCheckReceipts 将已验证检查观测转换为绑定本次远程运行身份的持久化回执。
func remoteRunCheckReceipts(
	result remoteci.RunResult,
	acceptedGeneration uint64,
	observations []cicontract.CheckObservation,
) ([]gatecontract.CheckReceiptRecord, error) {
	if result.JobID == "" || result.SourceTreeSHA == "" {
		return nil, errors.New("remote CI check receipt identity is missing")
	}
	if err := cicontract.ValidateAgentTokenDigest(result.AgentTokenDigest); err != nil {
		return nil, fmt.Errorf("remote CI result agent token digest: %w", err)
	}
	if acceptedGeneration == 0 {
		return nil, errors.New("remote CI accepted generation is missing")
	}
	receipts := make([]gatecontract.CheckReceiptRecord, 0, len(observations))
	for _, observation := range observations {
		receipt := gatecontract.CheckReceiptRecord{
			RunID: result.JobID, JobID: result.JobID, CandidateTreeSHA: result.SourceTreeSHA, AgentTokenDigest: result.AgentTokenDigest,
			Force:              result.Force,
			AcceptedGeneration: acceptedGeneration, AcceptedSnapshotID: observation.AcceptedSnapshotID,
			RequiredCheck: observation.Check, Executed: observation.Executed, Reused: observation.Reused, ReuseProofSHA256: observation.ReuseProofSHA256, Passed: observation.Passed,
			StartedAt: time.UnixMilli(observation.StartedAtUnixMS).UTC(), CompletedAt: time.UnixMilli(observation.CompletedAtUnixMS).UTC(),
			Duration: time.Duration(observation.DurationMS) * time.Millisecond,
		}
		if receipt.AcceptedSnapshotID == "" {
			return nil, errors.New("remote CI accepted snapshot is missing")
		}
		sha256, err := gatecontract.CheckReceiptSHA256(receipt)
		if err != nil {
			return nil, fmt.Errorf("hash remote CI check receipt %q: %w", observation.Check, err)
		}
		receipt.ReceiptSHA256 = sha256
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

// validateRemoteRunStoredCheckReceipts 校验权威账本精确保存了本次调用生成的检查回执。
func validateRemoteRunStoredCheckReceipts(
	store *gatecontract.DurationLedgerStore,
	jobID string,
	want []gatecontract.CheckReceiptRecord,
) error {
	if store == nil {
		return errors.New("remote CI duration ledger store is required")
	}
	got, err := store.LoadCheckReceipts(jobID)
	if err != nil {
		return fmt.Errorf("reload remote CI check receipts: %w", err)
	}
	if len(got) != len(want) {
		return fmt.Errorf("reloaded remote CI check receipt count = %d, want %d", len(got), len(want))
	}
	wantByCheck := make(map[cicontract.RequiredCheck]gatecontract.CheckReceiptRecord, len(want))
	for _, receipt := range want {
		if _, duplicate := wantByCheck[receipt.RequiredCheck]; duplicate {
			return fmt.Errorf("expected remote CI check receipt %q is duplicated", receipt.RequiredCheck)
		}
		wantByCheck[receipt.RequiredCheck] = receipt
	}
	for _, receipt := range got {
		expected, found := wantByCheck[receipt.RequiredCheck]
		if !found || receipt != expected {
			return fmt.Errorf("reloaded remote CI check receipt %q does not exactly match this invocation", receipt.RequiredCheck)
		}
		delete(wantByCheck, receipt.RequiredCheck)
	}
	if len(wantByCheck) != 0 {
		return errors.New("reloaded remote CI check receipt collection is incomplete")
	}
	return nil
}

// remoteRunIsFullAuthoritativeAcceptance 判断完整的暂定运行形态是否可由唯一 SQLite 权威最终化。
func remoteRunIsFullAuthoritativeAcceptance(
	catalog gatecontract.WorkloadCatalog,
	result remoteci.RunResult,
) bool {
	return catalog.Authoritative && !result.Authoritative
}
