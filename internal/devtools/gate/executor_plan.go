package gate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	ExecutorPlanReportChunkPrefix = "SUPER_DOLPHIN_GATE_PLAN_REPORT_CHUNK "
	executorPlanReportChunkBytes  = 3 * 1024
	executorPlanMaxReportChunks   = 10000
	executorPlanMaxLogBytes       = 1 << 20
	executorPlanLaneCount         = 2
)

// PlanGateExecution 是 executor 对单个 gate 的有界、未签名观察结果。
type PlanGateExecution struct {
	GateID      GateID       `json:"gate_id"`
	Status      ResultStatus `json:"status"`
	ExitCode    int          `json:"exit_code"`
	StartedAt   time.Time    `json:"started_at"`
	CompletedAt time.Time    `json:"completed_at"`
	ArgvDigest  string       `json:"argv_digest"`
	Log         []byte       `json:"log"`
	LogDigest   string       `json:"log_digest"`
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
	return []string{containerExecutorBinary, "run-plan", "--profile", string(plan.Profile),
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
	return []string{containerExecutorBinary, "run-shard", "--profile", string(plan.Profile),
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

const canonicalContainerShardGroupCount = 3

func validateContainerShardGateIDs(profile Profile, gateIDs []GateID) error {
	for _, expected := range canonicalContainerShardGroups(profile) {
		if slices.Equal(gateIDs, expected) {
			return nil
		}
	}
	return errors.New("shard gate list does not match a canonical container shard")
}

// canonicalContainerShardGroups is available to the standalone executor binary as well as the coordinator.
// 按稳定规则将必需 gate 分配给容器分片，并在发布配置中排除发布层 gate；仅返回非空分组。
func canonicalContainerShardGroups(profile Profile) [][]GateID {
	ids := requiredGateIDs(profile)
	if profile == ProfileRelease {
		ids = ids[:len(ids)-1]
	}
	groups := make([][]GateID, canonicalContainerShardGroupCount)
	for _, id := range ids {
		switch id {
		case GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify:
			groups[0] = append(groups[0], id)
		case GateIDBackendTestWithGuard, GateIDLSPChangedDiagnostics, GateIDBackendTestGuardWithRace, GateIDBackendNilness:
			groups[1] = append(groups[1], id)
		default:
			groups[2] = append(groups[2], id)
		}
	}
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
	runGate := func(ctx context.Context, laneIndex int, id GateID) (PlanGateExecution, error) {
		return executePlanGate(ctx, laneIndex, id, time.Now)
	}
	return executeGatePlanWithRunner(ctx, request, runGate, time.Now)
}

type executorPlanGateRunner func(context.Context, int, GateID) (PlanGateExecution, error)

// executeGatePlanWithRunner 按固定 lane DAG 执行并以 canonical 顺序汇总每个 gate。
func executeGatePlanWithRunner(
	ctx context.Context,
	request executorPlanRequest,
	runGate executorPlanGateRunner,
	now func() time.Time,
) (PlanExecutionReport, error) {
	report := PlanExecutionReport{SchemaVersion: 1, Profile: request.profile, PlanDigest: request.planDigest}
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
		encoded, err := json.Marshal(spec.Argv)
		if err != nil {
			return "", fmt.Errorf("marshal gate %q argv: %w", gateID, err)
		}
		digest := sha256.Sum256(encoded)
		return fmt.Sprintf("sha256:%x", digest), nil
	}
	return "", fmt.Errorf("gate %q is not canonical for profile %q", gateID, profile)
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
	now func() time.Time,
) (PlanGateExecution, error) {
	if now == nil {
		return PlanGateExecution{}, errors.New("gate clock is required")
	}
	program, ok := executorPrograms[id]
	if !ok {
		return PlanGateExecution{}, fmt.Errorf("plan gate %q has no executor program", id)
	}
	workRoot := executorPlanLaneRoot(ExecutorWorkRoot, laneIndex)
	if err := os.MkdirAll(workRoot, 0o700); err != nil {
		return PlanGateExecution{}, err
	}
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	config := executorConfig{
		sourcePath: ExecutorSourcePath, workRoot: workRoot, searchPath: executorSearchPath,
		expectedUID: executorUID, requireReadOnlySource: true,
		runtimeSeedRoot: ExecutorRuntimeSeedRoot, runtimeSeedManifest: ExecutorRuntimeSeedManifestPath,
		stdout: log, stderr: log,
	}
	result := PlanGateExecution{GateID: id, StartedAt: now().UTC(), ExitCode: -1}
	err := executeProgram(ctx, config, id, cloneExecutorProgram(program))
	result.CompletedAt = now().UTC()
	result.Status, result.ExitCode = classifyPlanGateOutcome(err, ctx.Err())
	if summary := planGateFailureSummary(err, ctx.Err(), result.Status, result.ExitCode); len(summary) != 0 {
		if _, writeErr := log.Write(summary); writeErr != nil {
			err = errors.Join(err, fmt.Errorf("persist gate failure summary: %w", writeErr))
		}
	}
	result.Log = log.Bytes()
	result.LogDigest = digestPlanLog(result.Log)
	return result, err
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
		{GateIDAIMaintenanceSelfTest, GateIDFrontendTest, GateIDLSPChangedDiagnostics, GateIDBackendTestWithGuard,
			GateIDBackendTestGuardWithRace, GateIDBackendNilness, GateIDReleaseLayeredCheck},
		{GateIDFrontendLint, GateIDFrontendBuild, GateIDFrontendEmbedVerify, GateIDSQLCVerify, GateIDCodemapCheck,
			GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck},
	}
	wanted := make(map[GateID]bool, len(gateIDs))
	for _, id := range gateIDs {
		wanted[id] = true
	}
	lanes := make([][]GateID, executorPlanLaneCount)
	seen := make(map[GateID]bool, len(gateIDs))
	for laneIndex, catalog := range laneCatalog {
		for _, id := range catalog {
			if wanted[id] {
				lanes[laneIndex] = append(lanes[laneIndex], id)
				seen[id] = true
			}
		}
	}
	if len(seen) != len(gateIDs) {
		return nil, errors.New("plan lane catalog does not cover every required gate")
	}
	return lanes, nil
}

func writePlanExecutionReport(writer io.Writer, report PlanExecutionReport) error {
	chunks, err := EncodePlanExecutionReportChunks(report)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := fmt.Fprintln(writer, chunk); err != nil {
			return err
		}
	}
	return nil
}

// EncodePlanExecutionReportChunks 将 report 编码为小于 Docker 日志分片阈值的 digest-bound 规范块。
func EncodePlanExecutionReportChunks(report PlanExecutionReport) ([]string, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	digest := digestPlanLog(data)
	reportID := strings.TrimPrefix(digest, "sha256:")[:32]
	total := (len(encoded) + executorPlanReportChunkBytes - 1) / executorPlanReportChunkBytes
	if total == 0 || total > executorPlanMaxReportChunks {
		return nil, errors.New("encoded plan report exceeds chunk framing limit")
	}
	chunks := make([]string, 0, total)
	for index := range total {
		start := index * executorPlanReportChunkBytes
		end := min(start+executorPlanReportChunkBytes, len(encoded))
		chunks = append(chunks, fmt.Sprintf("%s%s %s %06d %06d %s",
			ExecutorPlanReportChunkPrefix, reportID, digest, index+1, total, encoded[start:end]))
	}
	return chunks, nil
}

// DecodePlanExecutionReportChunks 严格重组同一 digest-bound report 的连续规范分块。
func DecodePlanExecutionReportChunks(chunks []string) (PlanExecutionReport, error) {
	if len(chunks) == 0 || len(chunks) > executorPlanMaxReportChunks {
		return PlanExecutionReport{}, errors.New("plan report chunk count is invalid")
	}
	reportID, reportDigest, encoded, err := joinPlanExecutionReportChunks(chunks)
	if err != nil {
		return PlanExecutionReport{}, err
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return PlanExecutionReport{}, fmt.Errorf("decode plan report chunks: %w", err)
	}
	if digestPlanLog(data) != reportDigest || strings.TrimPrefix(reportDigest, "sha256:")[:32] != reportID {
		return PlanExecutionReport{}, errors.New("plan report chunk digest does not match reassembled payload")
	}
	return decodePlanExecutionReportData(data)
}

// joinPlanExecutionReportChunks 验证分块身份与连续序号后重组 base64 payload。
func joinPlanExecutionReportChunks(chunks []string) (string, string, string, error) {
	var reportID, reportDigest string
	var encoded strings.Builder
	for index, chunk := range chunks {
		id, digest, sequence, total, payload, err := parsePlanExecutionReportChunk(chunk)
		if err != nil {
			return "", "", "", err
		}
		if index == 0 {
			reportID, reportDigest = id, digest
		}
		if id != reportID || digest != reportDigest || total != len(chunks) || sequence != index+1 {
			return "", "", "", errors.New("plan report chunks are missing, duplicated, reordered, or mixed")
		}
		encoded.WriteString(payload)
	}
	return reportID, reportDigest, encoded.String(), nil
}

// parsePlanExecutionReportChunk 解析并验证单个 canonical report frame。
func parsePlanExecutionReportChunk(chunk string) (string, string, int, int, string, error) {
	if !strings.HasPrefix(chunk, ExecutorPlanReportChunkPrefix) {
		return "", "", 0, 0, "", errors.New("plan report chunk prefix is invalid")
	}
	body := strings.TrimSuffix(strings.TrimPrefix(chunk, ExecutorPlanReportChunkPrefix), "\n")
	fields := strings.Split(body, " ")
	if err := validatePlanReportChunkHeader(fields, body); err != nil {
		return "", "", 0, 0, "", errors.New("plan report chunk header is invalid")
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", "", 0, 0, "", errors.New("plan report chunk id is invalid")
	}
	sequence, sequenceErr := parsePlanReportChunkNumber(fields[2])
	total, totalErr := parsePlanReportChunkNumber(fields[3])
	if !validPlanReportChunkSequence(sequence, total, sequenceErr, totalErr, fields[4]) {
		return "", "", 0, 0, "", errors.New("plan report chunk sequence is invalid")
	}
	return fields[0], fields[1], sequence, total, fields[4], nil
}

func validatePlanReportChunkHeader(fields []string, body string) error {
	if len(fields) != 5 || strings.Join(fields, " ") != body {
		return errors.New("plan report chunk fields are invalid")
	}
	if len(fields[0]) != 32 || !digestPattern.MatchString(fields[1]) {
		return errors.New("plan report chunk identity is invalid")
	}
	return nil
}

func validPlanReportChunkSequence(sequence int, total int, sequenceErr error, totalErr error, payload string) bool {
	return sequenceErr == nil && totalErr == nil && sequence <= total &&
		total <= executorPlanMaxReportChunks && payload != ""
}

func parsePlanReportChunkNumber(value string) (int, error) {
	if len(value) != 6 || value[0] == '0' && value == "000000" {
		return 0, errors.New("plan report chunk number is invalid")
	}
	return strconv.Atoi(value)
}

// DecodePlanExecutionReport 严格解码 host 从容器日志提取的 plan report。
func DecodePlanExecutionReport(encoded string) (PlanExecutionReport, error) {
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return PlanExecutionReport{}, fmt.Errorf("decode plan report base64: %w", err)
	}
	return decodePlanExecutionReportData(data)
}

// decodePlanExecutionReportData 解码 strict JSON 并验证 header 与 exact gate set。
func decodePlanExecutionReportData(data []byte) (PlanExecutionReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var report PlanExecutionReport
	if err := decoder.Decode(&report); err != nil {
		return PlanExecutionReport{}, fmt.Errorf("decode plan report: %w", err)
	}
	if err := rejectPlanReportTrailer(decoder); err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validatePlanExecutionReportHeader(report); err != nil {
		return PlanExecutionReport{}, err
	}
	if err := validatePlanExecutionReportGates(report); err != nil {
		return PlanExecutionReport{}, err
	}
	return report, nil
}

func validatePlanExecutionReportHeader(report PlanExecutionReport) error {
	if report.SchemaVersion != 1 || report.Profile.Validate() != nil || !digestPattern.MatchString(report.PlanDigest) {
		return errors.New("plan report header is invalid")
	}
	return nil
}

// validatePlanExecutionReportGates 验证完整 canonical plan 或单个 canonical shard 的精确结果集。
func validatePlanExecutionReportGates(report PlanExecutionReport) error {
	want := requiredGateIDs(report.Profile)
	observed := make([]GateID, len(report.Gates))
	for index, result := range report.Gates {
		observed[index] = result.GateID
	}
	if !slices.Equal(observed, want) {
		if err := validateContainerShardGateIDs(report.Profile, observed); err != nil {
			return errors.New("plan report does not contain a canonical plan or shard gate set")
		}
	}
	for _, result := range report.Gates {
		if !validPlanGateResult(result) {
			return errors.New("plan gate result is invalid")
		}
	}
	return nil
}

// 仅当 gate 日志、摘要、时间顺序和状态与退出码组合均有效时返回 true；任一不满足即拒绝该结果。
func validPlanGateResult(result PlanGateExecution) bool {
	return len(result.Log) <= executorPlanMaxLogBytes && result.LogDigest == digestPlanLog(result.Log) &&
		!result.StartedAt.IsZero() && !result.CompletedAt.IsZero() && !result.CompletedAt.Before(result.StartedAt) &&
		validPlanGateExit(result.Status, result.ExitCode)
}

func rejectPlanReportTrailer(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("plan report contains trailing JSON")
		}
		return fmt.Errorf("decode plan report trailer: %w", err)
	}
	return nil
}

func validPlanGateExit(status ResultStatus, exitCode int) bool {
	switch status {
	case ResultStatusPassed:
		return exitCode == 0
	case ResultStatusFailed:
		return exitCode > 0
	case ResultStatusCancelled, ResultStatusTimeout:
		return exitCode == -1
	default:
		return false
	}
}

type boundedPlanLog struct {
	mu        sync.Mutex
	remaining int
	data      []byte
}

func newBoundedPlanLog(limit int) *boundedPlanLog {
	return &boundedPlanLog{remaining: limit}
}

// Write 保留输入长度语义并只记录剩余证据预算内的字节。
func (log *boundedPlanLog) Write(value []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	written := len(value)
	if len(value) > log.remaining {
		value = value[:log.remaining]
	}
	log.data = append(log.data, value...)
	log.remaining -= len(value)
	return written, nil
}

// Bytes 返回当前有界日志的并发安全副本。
func (log *boundedPlanLog) Bytes() []byte {
	log.mu.Lock()
	defer log.mu.Unlock()
	return bytes.Clone(log.data)
}

func digestPlanLog(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}
