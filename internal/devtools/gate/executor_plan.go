package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
	"golang.org/x/sync/errgroup"
)

const (
	executorPlanReportSchemaVersion    = 4
	executorPlanTimingSchemaVersion    = 2
	ExecutorPlanReportChunkPrefix      = "SUPER_DOLPHIN_GATE_PLAN_REPORT_CHUNK "
	ExecutorWorkloadTimeoutEnvironment = "SUPER_DOLPHIN_REMOTE_EXECUTION_TIMEOUT"
	executorPlanReportChunkBytes       = 768
	executorPlanReportMaxLineBytes     = 1024
	executorPlanMaxTransportRecords    = 2000
	executorPlanReportMaxOutputBytes   = 1 << 20
	executorPlanMaxLogBytes            = 32 << 10
	executorPlanMaxLogLines            = 2000
	executorPlanMaxTimingRecords       = 2000
	executorPlanMaxLogRecords          = (executorPlanMaxLogBytes*2 + executorPlanReportChunkBytes - 1) / executorPlanReportChunkBytes
	executorPlanLaneCount              = 2
)

// ValidateExecutorWorkloadTimeout 只接受普通与 release 的固定安全上限；100 秒仅用于优化告警。
func ValidateExecutorWorkloadTimeout(timeout time.Duration) error {
	switch timeout {
	case 10 * time.Minute, 30 * time.Minute:
		return nil
	default:
		return fmt.Errorf("executor workload timeout %s is not registered", timeout)
	}
}

// GoTestStatus 是 worker 报告中的测试终态。
type GoTestStatus = testtiming.Status

const (
	GoTestStatusPass = testtiming.StatusPass
	GoTestStatusFail = testtiming.StatusFail
	GoTestStatusSkip = testtiming.StatusSkip
)

// GoTestTiming 是 worker 报告中的单测试或子测试耗时。
type GoTestTiming = testtiming.Timing

// PlanGateExecution 是 executor 对单个 gate 的有界、未签名观察结果。
type PlanGateExecution struct {
	GateID           GateID           `json:"gate_id"`
	Status           ResultStatus     `json:"status"`
	ExitCode         int              `json:"exit_code"`
	StartedAt        time.Time        `json:"started_at"`
	CompletedAt      time.Time        `json:"completed_at"`
	ArgvDigest       string           `json:"argv_digest"`
	Log              PlainTextLog     `json:"log"`
	LogDigest        string           `json:"log_digest"`
	TestTimings      []GoTestTiming   `json:"test_timings,omitempty"`
	ExecutionProfile ExecutionProfile `json:"execution_profile"`
}

// ExecutionProfile is the bounded, receipt-bound execution evidence for one gate.
// A v4 report never infers a cache hit or a remote phase from an omitted field.
type ExecutionProfile struct {
	CandidateTestBinaryPackage string            `json:"candidate_test_binary_package,omitempty"`
	CandidateTestBinaryMode    string            `json:"candidate_test_binary_mode,omitempty"`
	CacheSource                string            `json:"cache_source"`
	CacheStatus                string            `json:"cache_status"`
	CacheMeasurement           string            `json:"cache_measurement"`
	PrivateHitCount            uint64            `json:"private_hit_count"`
	BaselineHitCount           uint64            `json:"baseline_hit_count"`
	BaselineHitByGeneration    map[string]uint64 `json:"baseline_hit_by_generation,omitempty"`
	CacheMissCount             uint64            `json:"cache_miss_count"`
	CachePutCount              uint64            `json:"cache_put_count"`
	MaterializeMS              int64             `json:"materialize_ms"`
	DownloadMS                 int64             `json:"download_ms"`
	VerifyMS                   int64             `json:"verify_ms"`
	StartupMS                  int64             `json:"startup_ms"`
	TestBodyMS                 int64             `json:"test_body_ms"`
	TotalMS                    int64             `json:"total_ms"`
}

// PlanExecutionReport 绑定 plan digest，并按 canonical plan 顺序汇总所有已观察 gate。
type PlanExecutionReport struct {
	SchemaVersion uint32              `json:"schema_version"`
	Profile       Profile             `json:"profile"`
	PlanDigest    string              `json:"plan_digest"`
	Gates         []PlanGateExecution `json:"gates"`
}

type executorPlanRequest struct {
	profile    Profile
	planDigest string
	gateIDs    []GateID
	shard      bool
}

type releaseAttestationPayload struct {
	SchemaVersion uint32                        `json:"schema_version"`
	Profile       Profile                       `json:"profile"`
	PlanDigest    string                        `json:"plan_digest"`
	Prerequisites []releaseAttestationGateProof `json:"prerequisites"`
}

type releaseAttestationGateProof struct {
	GateID    GateID       `json:"gate_id"`
	Status    ResultStatus `json:"status"`
	ExitCode  int          `json:"exit_code"`
	LogDigest string       `json:"log_digest"`
}

// PlanExecutorArgv 从已验证计划生成唯一容器 argv。
func PlanExecutorArgv(plan GatePlan) ([]string, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	gateIDs := make([]string, len(plan.Gates))
	for index, spec := range plan.Gates {
		gateIDs[index] = string(spec.ID)
	}
	return []string{containerGateBinary, containerWorkerNamespace, "run-plan", "--profile", string(plan.Profile),
		"--plan-digest", plan.PlanDigest, "--gates", strings.Join(gateIDs, ",")}, nil
}

// ContainerShardExecutorArgv 将一次 disposable container 绑定到精确 canonical shard。
func ContainerShardExecutorArgv(plan GatePlan, gateIDs []GateID) ([]string, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if err := validateContainerShardGateIDs(plan.Profile, gateIDs); err != nil {
		return nil, err
	}
	values := make([]string, len(gateIDs))
	for index, id := range gateIDs {
		values[index] = string(id)
	}
	return []string{containerGateBinary, containerWorkerNamespace, "run-shard", "--profile", string(plan.Profile),
		"--plan-digest", plan.PlanDigest, "--gates", strings.Join(values, ",")}, nil
}

// ExecutePlanExecutor 严格解析 plan argv，在两个隔离 lane 中执行完整 required gate 集。
func ExecutePlanExecutor(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	request, err := parseExecutorPlanCommand(args)
	if err != nil {
		return err
	}
	report, executionErr := executeGatePlan(ctx, request)
	if err := writePlanExecutionReport(stdout, report); err != nil {
		return errors.Join(executionErr, err)
	}
	return executionErr
}

// parseExecutorPlanCommand 严格解析 profile、计划摘要与 canonical gate 集合。
func parseExecutorPlanCommand(args []string) (executorPlanRequest, error) {
	if len(args) != 7 || !isPlanExecutorCommand(args[0]) || args[1] != "--profile" ||
		args[3] != "--plan-digest" || args[5] != "--gates" {
		return executorPlanRequest{}, errors.New("usage: run-plan --profile <profile> --plan-digest <sha256> --gates <canonical-list>")
	}
	request := executorPlanRequest{profile: Profile(args[2]), planDigest: args[4]}
	if err := request.profile.Validate(); err != nil {
		return executorPlanRequest{}, err
	}
	if !digestPattern.MatchString(request.planDigest) {
		return executorPlanRequest{}, errors.New("plan digest is invalid")
	}
	for raw := range strings.SplitSeq(args[6], ",") {
		request.gateIDs = append(request.gateIDs, GateID(raw))
	}
	request.shard = args[0] == "run-shard"
	if err := validateExecutorGateIDs(request.profile, request.gateIDs, request.shard); err != nil {
		return executorPlanRequest{}, err
	}
	return request, nil
}

func isPlanExecutorCommand(command string) bool {
	return command == "run-plan" || command == "run-shard"
}

func validateExecutorGateIDs(profile Profile, gateIDs []GateID, shard bool) error {
	if shard {
		return validateContainerShardGateIDs(profile, gateIDs)
	}
	return validatePlanGateIDs(profile, gateIDs)
}

// validateContainerShardGateIDs 校验 worker 只执行当前 profile 的唯一 required-gate 子集。
// 完整覆盖和具体分组由 coordinator 冻结的 ContainerShardSet 负责校验。
func validateContainerShardGateIDs(profile Profile, gateIDs []GateID) error {
	if len(gateIDs) == 0 {
		return errors.New("shard gate list is empty")
	}
	required := requiredContainerShardGateIDs(profile)
	requiredSet := make(map[GateID]struct{}, len(required))
	for _, id := range required {
		requiredSet[id] = struct{}{}
	}
	seen := make(map[GateID]struct{}, len(gateIDs))
	for _, id := range gateIDs {
		parent, err := workloadParentGateID(string(id))
		if err != nil {
			return err
		}
		if _, ok := requiredSet[parent]; !ok {
			return fmt.Errorf("shard gate %q is not worker-executable for profile %q", id, profile)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("shard gate %q is duplicated", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// validateCanonicalReportShardGateIDs 确认报告的 gate 序列恰好等于某个规范 container shard。
func validateCanonicalReportShardGateIDs(profile Profile, gateIDs []GateID) error {
	for count := uint8(1); count <= MaxContainerShards && int(count) <= len(requiredContainerShardGateIDs(profile)); count++ {
		for _, expected := range canonicalContainerShardGroups(profile, count) {
			if slices.Equal(gateIDs, expected) {
				return nil
			}
		}
	}
	return errors.New("shard gate list does not match a canonical container shard")
}

// canonicalContainerShardGroups 是 coordinator 与 executor 共享的分片合同。
// 三分片保持历史分组不变；其他数量仅在原组内切分，并在至少两个分片时隔离 race gate。
func canonicalContainerShardGroups(profile Profile, shardsPerJob uint8) [][]GateID {
	if err := validateRequestedContainerShardCount(shardsPerJob, len(requiredContainerShardGateIDs(profile))); err != nil {
		return nil
	}
	groups := legacyCanonicalContainerShardGroups(profile)
	switch shardsPerJob {
	case 1:
		return [][]GateID{mergeContainerShardGroups(groups)}
	case legacyContainerShardCount:
		return groups
	case 2:
		return [][]GateID{mergeContainerShardGroups(groups[:2]), slices.Clone(groups[2])}
	}
	for uint8(len(groups)) < shardsPerJob {
		index := largestSplittableContainerShardGroup(groups)
		if index < 0 {
			return nil
		}
		left, right := splitContainerShardGroup(groups[index])
		groups = append(groups, nil)
		copy(groups[index+2:], groups[index+1:])
		groups[index], groups[index+1] = left, right
	}
	return groups
}

func mergeContainerShardGroups(groups [][]GateID) []GateID {
	merged := make([]GateID, 0)
	for _, group := range groups {
		merged = append(merged, group...)
	}
	return merged
}

func requiredContainerShardGateIDs(profile Profile) []GateID {
	ids := requiredGateIDs(profile)
	if profile == ProfileRelease {
		ids = ids[:len(ids)-1]
	}
	return ids
}

func legacyCanonicalContainerShardGroups(profile Profile) [][]GateID {
	ids := requiredContainerShardGateIDs(profile)
	isolateRace := slices.Contains(ids, GateIDBackendTestGuardWithRace)
	groups := make([][]GateID, legacyContainerShardCount)
	for _, id := range ids {
		index := canonicalContainerShardGroupIndex(profile, id, isolateRace)
		groups[index] = append(groups[index], id)
	}
	return nonemptyContainerShardGroups(groups)
}

func largestSplittableContainerShardGroup(groups [][]GateID) int {
	index := -1
	for candidate, group := range groups {
		if len(group) > 1 && (index < 0 || len(group) > len(groups[index])) {
			index = candidate
		}
	}
	return index
}

func splitContainerShardGroup(group []GateID) ([]GateID, []GateID) {
	middle := (len(group) + 1) / 2
	return slices.Clone(group[:middle]), slices.Clone(group[middle:])
}

// canonicalContainerShardGroupIndex 按配置、门禁类型与 race 隔离策略返回固定 shard 索引。
func canonicalContainerShardGroupIndex(profile Profile, id GateID, isolateRace bool) int {
	switch id {
	case GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendFullTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify:
		return 0
	case GateIDLSPChangedDiagnostics:
		if profile == ProfilePush {
			return 0
		}
		return 1
	case GateIDBackendTestWithGuard, GateIDBackendNilness:
		return 1
	case GateIDBackendTestGuardWithRace:
		if isolateRace {
			return 2
		}
		return 1
	default:
		if isolateRace {
			return 0
		}
		return 2
	}
}

func nonemptyContainerShardGroups(groups [][]GateID) [][]GateID {
	nonempty := make([][]GateID, 0, len(groups))
	for _, group := range groups {
		if len(group) != 0 {
			nonempty = append(nonempty, group)
		}
	}
	return nonempty
}

func validatePlanGateIDs(profile Profile, gateIDs []GateID) error {
	want := requiredGateIDs(profile)
	if !slices.Equal(gateIDs, want) {
		return errors.New("plan gate list does not match the canonical required profile")
	}
	return nil
}

func requiredGateIDs(profile Profile) []GateID {
	var ids []GateID
	for _, spec := range GateRegistry() {
		if slices.Contains(spec.RequiredProfiles, profile) {
			ids = append(ids, spec.ID)
		}
	}
	return ids
}

func executeGatePlan(ctx context.Context, request executorPlanRequest) (PlanExecutionReport, error) {
	preparedRuntimeSeeds, err := prepareExecutorPlanRuntimeSeeds(request.gateIDs)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	goBuildCacheRoot, goBuildCacheSeedRoots, err := prepareExecutorPlanGoBuildCache(request.gateIDs)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	executionCtx, cancelExecution := executorWorkloadContext(ctx)
	defer cancelExecution()
	runGate := func(ctx context.Context, laneIndex int, id GateID) (PlanGateExecution, error) {
		return executePlanGate(
			ctx,
			laneIndex,
			id,
			preparedRuntimeSeeds,
			goBuildCacheRoot,
			goBuildCacheSeedRoots,
			time.Now,
		)
	}
	report, executionErr := executeGatePlanWithRunner(executionCtx, request, runGate, time.Now)
	if goBuildCacheRoot != "" {
		executionErr = errors.Join(executionErr, removeExecutorWorkspacePath(goBuildCacheRoot))
	}
	return report, executionErr
}

func prepareExecutorPlanRuntimeSeeds(gateIDs []GateID) (*executorPreparedRuntimeSeeds, error) {
	needsGoSeed := false
	needsFrontendSeed := false
	for _, id := range gateIDs {
		_, program, err := executorProgramForWorkload(id)
		if err != nil {
			return nil, err
		}
		needsGoSeed = needsGoSeed || program.NeedsGoSeed
		needsFrontendSeed = needsFrontendSeed || program.NeedsFrontendSeed
	}
	return prepareExecutorRuntimeSeeds(
		ExecutorRuntimeSeedRoot,
		ExecutorRuntimeSeedManifestPath,
		needsGoSeed,
		needsFrontendSeed,
	)
}

// prepareExecutorPlanGoBuildCache 为包含 Go workload 的分片创建一次私有构建缓存写层。
func prepareExecutorPlanGoBuildCache(gateIDs []GateID) (string, []string, error) {
	return prepareExecutorPlanGoBuildCacheAt(
		gateIDs,
		ExecutorWorkRoot,
		ExecutorGoBuildCacheSeedsRoot,
		ExecutorGoBuildCacheSeedRoot,
	)
}

// prepareExecutorPlanGoBuildCacheAt 只为含 Go workload 的分片发现共享 seed 并创建私有写层。
func prepareExecutorPlanGoBuildCacheAt(
	gateIDs []GateID,
	workRoot string,
	seedGenerationsRoot string,
	legacySeedRoot string,
) (string, []string, error) {
	needsGoCache := false
	for _, id := range gateIDs {
		_, program, err := executorProgramForWorkload(id)
		if err != nil {
			return "", nil, err
		}
		needsGoCache = needsGoCache || program.NeedsGoSeed
	}
	if !needsGoCache {
		return "", nil, nil
	}
	seedRoots, err := discoverExecutorGoBuildCacheSeedRoots(seedGenerationsRoot, legacySeedRoot)
	if err != nil {
		return "", nil, err
	}
	cacheRoot := filepath.Join(workRoot, "plan-go-cache")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil {
		return "", nil, fmt.Errorf("create plan Go build cache: %w", err)
	}
	if err := seedExecutorGoBuildCacheSeeds(seedRoots, cacheRoot); err != nil {
		return "", nil, errors.Join(err, removeExecutorWorkspacePath(cacheRoot))
	}
	return cacheRoot, seedRoots, nil
}

type executorPlanGateRunner func(context.Context, int, GateID) (PlanGateExecution, error)

// executeGatePlanWithRunner 按固定 lane DAG 执行并以 canonical 顺序汇总每个 gate。
func executeGatePlanWithRunner(
	ctx context.Context,
	request executorPlanRequest,
	runGate executorPlanGateRunner,
	now func() time.Time,
) (PlanExecutionReport, error) {
	report := PlanExecutionReport{SchemaVersion: executorPlanReportSchemaVersion, Profile: request.profile, PlanDigest: request.planDigest}
	if now == nil {
		return report, errors.New("plan clock is required")
	}
	prerequisiteGateIDs, requiresReleaseAttestation, err := planExecutionPrerequisites(request)
	if err != nil {
		return report, err
	}
	lanes, err := executorPlanLanes(prerequisiteGateIDs)
	if err != nil {
		return report, err
	}
	workers, planCtx := errgroup.WithContext(ctx)
	observed := make(map[GateID]PlanGateExecution, len(request.gateIDs))
	var observedMu sync.Mutex
	for index, lane := range lanes {
		laneIndex := index
		laneGateIDs := slices.Clone(lane)
		workers.Go(func() error {
			return runExecutorPlanLane(planCtx, laneIndex, laneGateIDs, observed, &observedMu, runGate)
		})
	}
	executionErr := workers.Wait()
	if executionErr == nil && requiresReleaseAttestation {
		result, attestationErr := executeReleaseLayerAttestation(request, observed, now)
		observed[GateIDReleaseLayeredCheck] = result
		executionErr = attestationErr
	}
	cancelledAt := now().UTC()
	for _, id := range request.gateIDs {
		if result, ok := observed[id]; ok {
			report.Gates = append(report.Gates, result)
			continue
		}
		status, log := pendingPlanGateResult(ctx)
		report.Gates = append(report.Gates, PlanGateExecution{
			GateID: id, Status: status, ExitCode: -1,
			StartedAt: cancelledAt, CompletedAt: cancelledAt,
			Log: log, LogDigest: digestPlanLog(log),
			ExecutionProfile: unmeasuredExecutionProfile(cancelledAt, cancelledAt),
		})
	}
	return report, executionErr
}

// planExecutionPrerequisites 校验 canonical 请求，并将 release 最终证明门禁从并行 lane 前置项中分离。
func planExecutionPrerequisites(request executorPlanRequest) ([]GateID, bool, error) {
	if err := request.profile.Validate(); err != nil {
		return nil, false, err
	}
	if !digestPattern.MatchString(request.planDigest) {
		return nil, false, errors.New("plan digest is invalid")
	}
	if err := validateExecutorGateIDs(request.profile, request.gateIDs, request.shard); err != nil {
		return nil, false, err
	}
	if request.shard {
		return slices.Clone(request.gateIDs), false, nil
	}
	gateIDs := slices.Clone(request.gateIDs)
	if request.profile != ProfileRelease {
		return gateIDs, false, nil
	}
	if len(gateIDs) == 0 || gateIDs[len(gateIDs)-1] != GateIDReleaseLayeredCheck {
		return nil, false, errors.New("release attestation must be the final canonical gate")
	}
	return gateIDs[:len(gateIDs)-1], true, nil
}

// executeReleaseLayerAttestation 在两个 lane 汇合后验证同进程内的 canonical 前序结果并生成增量证明。
func executeReleaseLayerAttestation(
	request executorPlanRequest,
	observed map[GateID]PlanGateExecution,
	now func() time.Time,
) (PlanGateExecution, error) {
	startedAt := now().UTC()
	log, err := canonicalReleaseAttestationLog(request, observed)
	if err != nil {
		return failedReleaseAttestation(startedAt, "prerequisite evidence is invalid", err)
	}
	argvDigest, err := canonicalGateArgvDigest(request.profile, GateIDReleaseLayeredCheck)
	if err != nil {
		return failedReleaseAttestation(startedAt, "release command identity is invalid", err)
	}
	completedAt := now().UTC()
	if !completedAt.After(startedAt) {
		completedAt = startedAt.Add(time.Nanosecond)
	}
	result := PlanGateExecution{
		GateID: GateIDReleaseLayeredCheck, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: startedAt, CompletedAt: completedAt, ArgvDigest: argvDigest, Log: log, LogDigest: digestPlanLog(log),
		ExecutionProfile: unmeasuredExecutionProfile(startedAt, completedAt),
	}
	if err := validateReleaseLayerAttestation(request, observed, result); err != nil {
		return failedReleaseAttestation(startedAt, "generated attestation is invalid", err)
	}
	return result, nil
}

// canonicalGateArgvDigest 返回 profile 内指定 gate 的规范命令摘要。
func canonicalGateArgvDigest(profile Profile, gateID GateID) (string, error) {
	for _, spec := range requiredGatesForProfile(profile) {
		if spec.ID != gateID {
			continue
		}
		return gateSpecArgvDigest(spec)
	}
	return "", fmt.Errorf("gate %q is not canonical for profile %q", gateID, profile)
}

func gateSpecArgvDigest(spec GateSpec) (string, error) {
	encoded, err := json.Marshal(spec.Argv)
	if err != nil {
		return "", fmt.Errorf("marshal gate %q argv: %w", spec.ID, err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// canonicalReleaseAttestationLog 生成按 canonical gate 顺序绑定的 release 前序证明日志。
func canonicalReleaseAttestationLog(
	request executorPlanRequest,
	observed map[GateID]PlanGateExecution,
) ([]byte, error) {
	prerequisiteGateIDs, required, err := planExecutionPrerequisites(request)
	if err != nil {
		return nil, err
	}
	if !required {
		return nil, errors.New("release attestation is only valid for the canonical release plan")
	}
	if len(observed) != len(prerequisiteGateIDs) {
		return nil, errors.New("release prerequisite result set is incomplete or contains an unexpected gate")
	}
	payload := releaseAttestationPayload{
		SchemaVersion: 1, Profile: request.profile, PlanDigest: request.planDigest,
		Prerequisites: make([]releaseAttestationGateProof, 0, len(prerequisiteGateIDs)),
	}
	for _, id := range prerequisiteGateIDs {
		gateResult, ok := observed[id]
		if err := validateReleasePrerequisiteEvidence(id, gateResult, ok); err != nil {
			return nil, err
		}
		payload.Prerequisites = append(payload.Prerequisites, releaseAttestationGateProof{
			GateID: id, Status: gateResult.Status, ExitCode: gateResult.ExitCode, LogDigest: gateResult.LogDigest,
		})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode release prerequisite evidence: %w", err)
	}
	prerequisiteDigest := digestPlanLog(encoded)
	return fmt.Appendf(nil,
		"[release-layer-attestation] schema=1 profile=%s plan_digest=%s prerequisite_digest=%s prerequisite_gates=%d\n",
		request.profile, request.planDigest, prerequisiteDigest, len(payload.Prerequisites)), nil
}

// validateReleaseLayerAttestation 重新生成 canonical 证明并拒绝状态、时钟或摘要漂移。
func validateReleaseLayerAttestation(
	request executorPlanRequest,
	observed map[GateID]PlanGateExecution,
	result PlanGateExecution,
) error {
	expectedLog, err := canonicalReleaseAttestationLog(request, observed)
	if err != nil {
		return err
	}
	if result.GateID != GateIDReleaseLayeredCheck || result.Status != ResultStatusPassed || result.ExitCode != 0 {
		return errors.New("release attestation result identity or status is invalid")
	}
	if result.StartedAt.IsZero() || !result.CompletedAt.After(result.StartedAt) {
		return errors.New("release attestation timestamps are invalid")
	}
	if !bytes.Equal(result.Log, expectedLog) || result.LogDigest != digestPlanLog(expectedLog) {
		return errors.New("release attestation canonical digest evidence is invalid")
	}
	return nil
}

func failedReleaseAttestation(startedAt time.Time, reason string, cause error) (PlanGateExecution, error) {
	log := fmt.Appendf(nil, "[release-layer-attestation] %s\n", reason)
	return PlanGateExecution{
		GateID: GateIDReleaseLayeredCheck, Status: ResultStatusFailed, ExitCode: 1,
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Nanosecond), Log: log, LogDigest: digestPlanLog(log),
		ExecutionProfile: unmeasuredExecutionProfile(startedAt, startedAt),
	}, cause
}

// validateReleasePrerequisiteEvidence 对单项 typed 结果执行完整、无默认值的前序证明校验。
func validateReleasePrerequisiteEvidence(id GateID, result PlanGateExecution, exists bool) error {
	if !exists || result.GateID != id {
		return fmt.Errorf("release prerequisite %q is missing or misidentified", id)
	}
	if result.Status != ResultStatusPassed || result.ExitCode != 0 {
		return fmt.Errorf("release prerequisite %q did not pass", id)
	}
	if len(result.Log) > executorPlanMaxLogBytes || result.LogDigest != digestPlanLog(result.Log) {
		return fmt.Errorf("release prerequisite %q log evidence is invalid", id)
	}
	if result.StartedAt.IsZero() || result.CompletedAt.IsZero() || result.CompletedAt.Before(result.StartedAt) {
		return fmt.Errorf("release prerequisite %q timestamps are invalid", id)
	}
	return nil
}

func pendingPlanGateResult(ctx context.Context) (ResultStatus, []byte) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ResultStatusTimeout, []byte("gate timed out before start because the profile deadline expired\n")
	}
	return ResultStatusCancelled, []byte("gate canceled before start because a companion gate failed\n")
}

func runExecutorPlanLane(
	ctx context.Context,
	laneIndex int,
	gateIDs []GateID,
	results map[GateID]PlanGateExecution,
	resultsMu *sync.Mutex,
	runGate executorPlanGateRunner,
) error {
	for _, id := range gateIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result, err := runGate(ctx, laneIndex, id)
		if result.ExecutionProfile.CacheMeasurement == "" && !result.StartedAt.IsZero() && !result.CompletedAt.IsZero() {
			result.ExecutionProfile = unmeasuredExecutionProfile(result.StartedAt, result.CompletedAt)
		}
		resultsMu.Lock()
		results[id] = result
		resultsMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

// executePlanGate 在 lane 私有工作区运行一个 gate 并生成有界日志证据。
func executePlanGate(
	ctx context.Context,
	laneIndex int,
	id GateID,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoots []string,
	now func() time.Time,
) (PlanGateExecution, error) {
	if now == nil {
		return PlanGateExecution{}, errors.New("gate clock is required")
	}
	_, program, err := executorProgramForWorkload(id)
	if err != nil {
		return PlanGateExecution{}, fmt.Errorf("plan gate %q has no executor program: %w", id, err)
	}
	var candidateTestBinaries candidateTestBinaryBundleIndex
	if isCandidateTestBinaryEligibleWorkload(id) {
		var loadErr error
		candidateTestBinaries, loadErr = loadCandidateTestBinaryBundleIndex(os.Getenv(ExecutorCandidateTestBinaryIndexEnvironment))
		if loadErr != nil || len(candidateTestBinaries.Binaries) == 0 {
			return PlanGateExecution{}, errors.New("candidate test binary index is required for exact Go test workload")
		}
		program, err = candidateTestBinaryExecutorProgram(id, candidateTestBinaries)
		if err != nil {
			return PlanGateExecution{}, fmt.Errorf("resolve candidate test binary for plan gate %q: %w", id, err)
		}
	}
	workRoot := executorPlanLaneRoot(ExecutorWorkRoot, laneIndex)
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return PlanGateExecution{}, err
	}
	cacheProxy, err := executorGoBuildCacheProxyLauncher()
	if err != nil {
		return PlanGateExecution{}, err
	}
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	stdout := io.Writer(log)
	var timingWriter *testtiming.EventWriter
	if isGoPackageTestWorkload(id) {
		timingWriter = testtiming.NewEventWriter(log)
		stdout = timingWriter
	}
	metricsPath := ""
	if isGoPackageTestWorkload(id) && !isCandidateTestBinaryEligibleWorkload(id) {
		metricsPath, err = GoBuildCacheProxyMetricsPathForInvocation(goBuildCacheRoot, fmt.Sprintf("lane-%d-%x", laneIndex, sha256.Sum256([]byte(string(id)))))
		if err != nil {
			return PlanGateExecution{}, err
		}
	}
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: executorUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		goRoot:                  ExecutorPortableGoRoot,
		preparedRuntimeSeeds:    preparedRuntimeSeeds,
		goBuildCacheSeedRoots:   append([]string(nil), goBuildCacheSeedRoots...),
		goBuildCacheRoot:        goBuildCacheRoot,
		goBuildCacheProxy:       cacheProxy,
		goBuildCacheMetricsPath: metricsPath,
		frontendEmbedSeedRoot:   ExecutorFrontendEmbedSeedRoot,
		stdout:                  stdout, stderr: log,
	}
	result := PlanGateExecution{GateID: id, StartedAt: now().UTC(), ExitCode: -1}
	err = executeProgram(ctx, config, id, cloneExecutorProgram(program))
	if timingWriter != nil {
		err = errors.Join(err, timingWriter.Close())
		result.TestTimings = timingWriter.Timings()
	}
	result.CompletedAt = now().UTC()
	result.ExecutionProfile, err = executionProfileForGate(id, candidateTestBinaries, result.TestTimings, result.StartedAt, result.CompletedAt)
	if metricsPath != "" {
		metrics, metricsErr := LoadGoBuildCacheProxyMetricsAt(goBuildCacheRoot, metricsPath, goBuildCacheSeedRoots)
		if metricsErr != nil {
			err = errors.Join(err, fmt.Errorf("load Go build cache metrics: %w", metricsErr))
		} else {
			result.ExecutionProfile.CacheSource, result.ExecutionProfile.CacheMeasurement = "go_build_cache", "measured"
			result.ExecutionProfile.PrivateHitCount, result.ExecutionProfile.BaselineHitCount = metrics.PrivateHitCount, metrics.BaselineHitCount
			result.ExecutionProfile.BaselineHitByGeneration, result.ExecutionProfile.CacheMissCount, result.ExecutionProfile.CachePutCount = metrics.BaselineHitByGeneration, metrics.MissCount, metrics.PutCount
			result.ExecutionProfile.CacheStatus = cacheStatusFromMetrics(metrics)
		}
	}
	result.Status, result.ExitCode = classifyPlanGateOutcome(err, ctx.Err())
	if summary := planGateFailureSummary(err, ctx.Err(), result.Status, result.ExitCode); len(summary) != 0 {
		if _, writeErr := log.Write(summary); writeErr != nil {
			err = errors.Join(err, fmt.Errorf("persist gate failure summary: %w", writeErr))
		}
	}
	if result.Status != ResultStatusPassed {
		result.Log = log.Bytes()
	}
	result.LogDigest = digestPlanLog(result.Log)
	return result, err
}

func cacheStatusFromMetrics(metrics GoBuildCacheProxyMetrics) string {
	if metrics.PrivateHitCount+metrics.BaselineHitCount > 0 {
		return "hit"
	}
	if metrics.MissCount > 0 {
		return "miss"
	}
	if metrics.PutCount > 0 {
		return "put"
	}
	return "not_applicable"
}

func executionProfileForGate(id GateID, binaries candidateTestBinaryBundleIndex, timings []GoTestTiming, started, completed time.Time) (ExecutionProfile, error) {
	profile := unmeasuredExecutionProfile(started, completed)
	if len(binaries.Binaries) != 0 && isCandidateTestBinaryEligibleWorkload(id) {
		_, kind, target, targeted, err := parseTargetWorkloadID(string(id))
		if err == nil && targeted {
			pkg := target
			if kind == workloadTargetGoTest {
				if parsed, parseErr := ParseGoTestTarget(target); parseErr == nil {
					pkg = parsed.Package
				}
			}
			profile.CandidateTestBinaryPackage, profile.CandidateTestBinaryMode = pkg, "test"
			profile.CacheSource, profile.CacheStatus, profile.CacheMeasurement = "candidate-test-binary", "hit", "measured"
		}
	}
	profile.TotalMS = completed.Sub(started).Milliseconds()
	profile.TotalMS = max(profile.TotalMS, 0)
	if isExactGoTestWorkload(id) {
		_, _, target, _, parseErr := parseTargetWorkloadID(string(id))
		if parseErr != nil {
			return ExecutionProfile{}, parseErr
		}
		testTarget, parseErr := ParseGoTestTarget(target)
		if parseErr != nil {
			return ExecutionProfile{}, parseErr
		}
		var matched []GoTestTiming
		for _, timing := range timings {
			if timing.Name == testTarget.Name {
				matched = append(matched, timing)
			}
		}
		if len(matched) != 1 || matched[0].DurationMS < 0 || matched[0].DurationMS > profile.TotalMS {
			return ExecutionProfile{}, errors.New("exact Go test execution profile timing is missing or invalid")
		}
		profile.TestBodyMS = matched[0].DurationMS
	}
	profile.StartupMS = profile.TotalMS - profile.TestBodyMS
	return profile, nil
}

func unmeasuredExecutionProfile(started, completed time.Time) ExecutionProfile {
	total := completed.Sub(started).Milliseconds()
	total = max(total, 0)
	return ExecutionProfile{CacheSource: "none", CacheStatus: "not_applicable", CacheMeasurement: "not_measured", StartupMS: total, TotalMS: total}
}

func isGoPackageTestWorkload(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && (kind == workloadTargetGoPackage || kind == workloadTargetGoTest)
}

func isExactGoTestWorkload(id GateID) bool {
	_, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && kind == workloadTargetGoTest
}

func isCandidateTestBinaryEligibleWorkload(id GateID) bool {
	parent, kind, _, targeted, err := parseTargetWorkloadID(string(id))
	return err == nil && targeted && kind == workloadTargetGoTest && parent == GateIDBackendTestWithGuard
}

// planGateFailureSummary 只记录稳定分类与退出码，不回显可能含秘密或宿主路径的原始错误。
func planGateFailureSummary(gateErr error, contextErr error, status ResultStatus, exitCode int) []byte {
	if gateErr == nil {
		return nil
	}
	reason := "execution-error"
	if errors.Is(contextErr, context.DeadlineExceeded) {
		reason = "deadline"
	} else if errors.Is(contextErr, context.Canceled) {
		reason = "peer-cancellation"
	}
	return fmt.Appendf(nil, "[gate-executor] outcome status=%s exit_code=%d reason=%s\n", status, exitCode, reason)
}

// classifyPlanGateOutcome 只用 gate error 与父 context authority 生成 canonical status/exit。
func classifyPlanGateOutcome(gateErr error, contextErr error) (ResultStatus, int) {
	if gateErr == nil {
		return ResultStatusPassed, 0
	}
	if errors.Is(contextErr, context.DeadlineExceeded) {
		return ResultStatusTimeout, -1
	}
	if errors.Is(contextErr, context.Canceled) {
		return ResultStatusCancelled, -1
	}
	return ResultStatusFailed, ExecutorExitCode(gateErr)
}

func executorPlanLaneRoot(workRoot string, laneIndex int) string {
	return filepath.Join(workRoot, "lanes", fmt.Sprintf("lane-%d", laneIndex))
}

// executorPlanLanes 将 exact gate 集合映射到固定、互不共享可写目录的 lane DAG。
func executorPlanLanes(gateIDs []GateID) ([][]GateID, error) {
	laneCatalog := [][]GateID{
		{GateIDAIMaintenanceSelfTest, GateIDFrontendTest, GateIDFrontendFullTest, GateIDLSPChangedDiagnostics, GateIDBackendTestWithGuard,
			GateIDBackendTestGuardWithRace, GateIDBackendNilness, GateIDReleaseLayeredCheck},
		{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
			GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	lanes := make([][]GateID, executorPlanLaneCount)
	wanted := make(map[GateID]bool, len(gateIDs))
	for _, id := range gateIDs {
		parent, err := workloadParentGateID(string(id))
		if err != nil {
			return nil, err
		}
		matched := false
		for laneIndex, catalog := range laneCatalog {
			if slices.Contains(catalog, parent) {
				lanes[laneIndex] = append(lanes[laneIndex], id)
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("plan lane catalog does not cover workload %q", id)
		}
		wanted[id] = true
	}
	if len(wanted) != len(gateIDs) {
		return nil, errors.New("plan lane catalog does not cover every required gate")
	}
	return lanes, nil
}

func digestPlanLog(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
