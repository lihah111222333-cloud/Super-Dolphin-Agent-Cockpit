package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate/testtiming"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// runCompileGroupGate 要求精确 Go test selector 使用批处理结果；仅非精确 selector
// 才按 compile-group artifact 或普通 gate 路径执行。
func runCompileGroupGate(
	ctx context.Context,
	laneIndex int,
	id GateID,
	batchedResults map[GateID]PlanGateExecution,
	artifacts map[GateID]compiledGroupArtifact,
	executions []CompileGroupExecution,
	preparedRuntimeSeeds *executorPreparedRuntimeSeeds,
	goBuildCacheRoot string,
	goBuildCacheSeedRoot string,
) (PlanGateExecution, error) {
	if isExactGoTestWorkload(id) {
		if _, ok := artifacts[id]; !ok {
			return failedCompileGroupSelector(id, executions, time.Now)
		}
		return requireCompiledSelectorBatchResult(id, batchedResults, executions)
	}
	if artifact, ok := artifacts[id]; ok {
		return executeCompiledSelector(ctx, laneIndex, id, artifact, time.Now)
	}
	if isCompileGroupSelector(id) {
		return failedCompileGroupSelector(id, executions, time.Now)
	}
	return executePlanGate(ctx, laneIndex, id, preparedRuntimeSeeds, goBuildCacheRoot, goBuildCacheSeedRoot, time.Now)
}

// executeCompileGroupBatches 按 manifest 冻结的 wave 执行每个独立 batch。
// 同一 wave 的普通 batch 并行，exclusive batch 各占唯一 serial wave；任何
// batch 失败都不会取消兄弟 batch，caller 会在所有结果收齐后统一汇总错误。
func executeCompileGroupBatches(
	ctx context.Context,
	groups []CompileGroup,
	artifacts map[GateID]compiledGroupArtifact,
	now func() time.Time,
) (map[GateID]PlanGateExecution, map[string]error) {
	results := make(map[GateID]PlanGateExecution)
	errorsByGroup := make(map[string]error)
	for _, group := range groups {
		if len(group.BatchPlan) == 0 || len(group.WorkloadIDs) == 0 {
			continue
		}
		artifact, ok := artifacts[group.WorkloadIDs[0]]
		if !ok {
			continue
		}
		batchResults, batchErr := executeCompileGroupBatchWaves(ctx, artifact, group, now)
		maps.Copy(results, batchResults)
		if batchErr != nil {
			errorsByGroup[group.GroupID] = batchErr
		}
	}
	return results, errorsByGroup
}

// executeCompileGroupBatchWaves 按 wave 并发运行 batch，并按 canonical batch
// 顺序合并结果与错误，避免完成时序影响 receipt 摘要。
func executeCompileGroupBatchWaves(ctx context.Context, artifact compiledGroupArtifact, group CompileGroup, now func() time.Time) (map[GateID]PlanGateExecution, error) {
	results := make(map[GateID]PlanGateExecution, len(group.WorkloadIDs))
	var allErr error
	start := 0
	for start < len(group.BatchPlan) {
		wave := group.BatchPlan[start].Wave
		end := start
		for end < len(group.BatchPlan) && group.BatchPlan[end].Wave == wave {
			end++
		}
		var wait sync.WaitGroup
		waveResults := make([]map[GateID]PlanGateExecution, end-start)
		waveErrs := make([]error, end-start)
		for batchIndex, batch := range group.BatchPlan[start:end] {
			wait.Add(1)
			safego.Go(ctx, nil, "gate.compile-group.batch."+batch.BatchID, func(batchCtx context.Context) {
				defer wait.Done()
				batchResults, batchErr := executeCompiledSelectorBatchForBatch(batchCtx, artifact, group, batch, now)
				waveResults[batchIndex], waveErrs[batchIndex] = batchResults, batchErr
			})
		}
		wait.Wait()
		for batchIndex := range waveResults {
			maps.Copy(results, waveResults[batchIndex])
			allErr = errors.Join(allErr, waveErrs[batchIndex])
		}
		start = end
	}
	return results, allErr
}

// compileGroupSupportsSelectorBatch 判断组是否可以安全地一次运行多个 Go test。
func compileGroupSupportsSelectorBatch(group CompileGroup) bool {
	if group.SemanticKey != CompileGroupSemanticGoTestNormal && group.SemanticKey != CompileGroupSemanticGoTestRace {
		return false
	}
	for _, id := range group.WorkloadIDs {
		if !isExactGoTestWorkload(id) {
			return false
		}
	}
	return true
}

// executeCompiledSelectorBatchForBatch 运行 manifest 冻结的一个共享 binary batch，
// 并严格把终端事件映射回该 batch 的 selector 集合。
func executeCompiledSelectorBatchForBatch(ctx context.Context, artifact compiledGroupArtifact, group CompileGroup, batch CompileGroupBatch, now func() time.Time) (map[GateID]PlanGateExecution, error) {
	if now == nil {
		return nil, errors.New("compiled selector batch clock is required")
	}
	argv, specs, err := compileGroupBatchCommandArgvForBatch(group, batch, artifact.binaryPath)
	if err != nil {
		batchGroup := compileGroupForBatch(group, batch)
		results, profileErr := failedCompiledSelectorBatchResults(batchGroup, argv, nil, err, now)
		return results, errors.Join(err, profileErr)
	}
	batchGroup := compileGroupForBatch(group, batch)
	observation := runCompiledSelectorBatchProcess(ctx, artifact, batch, argv, specs, now)
	if observationErr := observation.err(); observationErr != nil {
		if observation.hasCompleteSelectorResults(specs) && observation.hasFailedSelectorResult(specs) {
			results, resultErr := compiledSelectorBatchResults(batchGroup, argv, specs, observation)
			return results, errors.Join(observationErr, resultErr)
		}
		results, profileErr := failedCompiledSelectorBatchResults(batchGroup, argv, &observation, observationErr, now)
		return results, errors.Join(observationErr, profileErr)
	}
	if err := observation.validateSelectorResults(specs); err != nil {
		results, profileErr := failedCompiledSelectorBatchResults(batchGroup, argv, &observation, err, now)
		return results, errors.Join(err, profileErr)
	}
	return compiledSelectorBatchResults(batchGroup, argv, specs, observation)
}

// compileGroupForBatch 返回只包含当前 batch selector 的结果映射视图。
func compileGroupForBatch(group CompileGroup, batch CompileGroupBatch) CompileGroup {
	batchGroup := group
	batchGroup.WorkloadIDs = append([]GateID(nil), batch.SelectorIDs...)
	return batchGroup
}

type compiledSelectorBatchSpec struct {
	id            GateID
	name          string
	packageTarget string
}

// compileGroupBatchCommandArgvForBatch 为单一 manifest batch 构造精确 selector 正则命令。
func compileGroupBatchCommandArgvForBatch(group CompileGroup, batch CompileGroupBatch, binaryPath string) ([]string, map[GateID]compiledSelectorBatchSpec, error) {
	if len(batch.SelectorIDs) == 0 {
		return nil, nil, errors.New("compiled selector batch requires at least one workload")
	}
	if !compileGroupSupportsSelectorBatch(group) {
		return nil, nil, errors.New("compiled selector batch group semantics are unsupported")
	}
	names := make([]string, 0, len(batch.SelectorIDs))
	specs := make(map[GateID]compiledSelectorBatchSpec, len(batch.SelectorIDs))
	seenNames := make(map[string]GateID, len(batch.SelectorIDs))
	for _, id := range batch.SelectorIDs {
		spec, err := selectorSpecForWorkload(id, group.PackageTarget)
		if err != nil {
			return nil, nil, err
		}
		if spec.kind != workloadTargetGoTest {
			return nil, nil, errors.New("compiled selector batch contains a non-Go-test workload")
		}
		if previous, duplicate := seenNames[spec.testName]; duplicate {
			return nil, nil, fmt.Errorf("compiled selector batch test %q is duplicated by %q and %q", spec.testName, previous, id)
		}
		seenNames[spec.testName] = id
		names = append(names, spec.testName)
		specs[id] = compiledSelectorBatchSpec{id: id, name: spec.testName, packageTarget: spec.packageTarget}
	}
	pattern := "^(" + strings.Join(quotedSelectorNames(names), "|") + ")$"
	testArgs := []string{"-test.v", "-test.run=" + pattern, "-test.count=1"}
	if group.SemanticKey == CompileGroupSemanticGoTestRace {
		// race compile group 的每个旧 selector 都带 -test.short，批量命令必须保留同一执行语义。
		testArgs = append([]string{"-test.short"}, testArgs...)
	}
	argv := append([]string{"go", "tool", "test2json", "-t", "-p", group.PackageTarget, binaryPath}, testArgs...)
	return argv, specs, nil
}

func quotedSelectorNames(values []string) []string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = regexp.QuoteMeta(value)
	}
	return quoted
}

type compiledSelectorBatchObservation struct {
	started, bodyStarted, completed time.Time
	selectorTimings                 map[string][]GoTestTiming
	selectorIntervals               map[string]compiledSelectorBatchInterval
	selectorLogs                    map[string][]byte
	log                             *boundedPlanLog
	runErr, closeErr, parseErr      error
	contextErr                      error
	extraTopLevel                   []string
}

var errCompiledSelectorBatchCleanup = errors.New("compiled selector batch cleanup")

// compiledSelectorBatchErrorKind 仅分类 batch 控制面错误，不暴露命令路径、
// 进程输出或其他不可信错误文本。
func compiledSelectorBatchErrorKind(observation compiledSelectorBatchObservation, batchErr error) string {
	if observation.contextErr != nil && errors.Is(batchErr, observation.contextErr) {
		return "context"
	}
	if observation.parseErr != nil && errors.Is(batchErr, observation.parseErr) {
		return "parse"
	}
	if observation.closeErr != nil && errors.Is(batchErr, observation.closeErr) {
		return "close"
	}
	if errors.Is(batchErr, errCompiledSelectorBatchCleanup) {
		return "cleanup"
	}
	if observation.runErr != nil && errors.Is(batchErr, observation.runErr) {
		return "run"
	}
	return "batch"
}

// compiledSelectorBatchErrorSummary 为报告写入有界的非敏感错误证据，同时保留
// 完整错误以维持执行失败结果。
func compiledSelectorBatchErrorSummary(observation compiledSelectorBatchObservation, batchErr error) []byte {
	if batchErr == nil {
		return nil
	}
	return fmt.Appendf(nil, "[gate-executor] batch-error kind=%s error-digest=%s\n", compiledSelectorBatchErrorKind(observation, batchErr), digestPlanLog([]byte(batchErr.Error())))
}

func appendCompiledSelectorBatchError(log []byte, summary []byte) []byte {
	if len(summary) == 0 {
		return log
	}
	bounded := newBoundedPlanLog(executorPlanMaxLogBytes)
	_, _ = bounded.Write(log)
	_, _ = bounded.Write(summary)
	return bounded.Bytes()
}

type compiledSelectorBatchInterval struct {
	runAt       time.Time
	completedAt time.Time
	paused      bool
	continued   bool
}

func (observation compiledSelectorBatchObservation) err() error {
	return errors.Join(observation.runErr, observation.closeErr, observation.parseErr, observation.contextErr)
}

// hasFailedSelectorResult 判断完整批量输出中是否有真实失败终态。
func (observation compiledSelectorBatchObservation) hasFailedSelectorResult(specs map[GateID]compiledSelectorBatchSpec) bool {
	for _, spec := range specs {
		for _, timing := range observation.selectorTimings[spec.name] {
			if timing.Name == spec.name && timing.Status == GoTestStatusFail {
				return true
			}
		}
	}
	return false
}

// hasCompleteSelectorResults 判断每个请求 selector 是否恰好有一个顶层终态。
func (observation compiledSelectorBatchObservation) hasCompleteSelectorResults(specs map[GateID]compiledSelectorBatchSpec) bool {
	if observation.closeErr != nil || observation.parseErr != nil || len(observation.extraTopLevel) != 0 {
		return false
	}
	for _, spec := range specs {
		if len(exactGoTestTimings(observation.selectorTimings[spec.name], spec.name)) != 1 {
			return false
		}
		interval, ok := observation.selectorIntervals[spec.name]
		if !ok || !interval.completedAt.After(interval.runAt) {
			return false
		}
	}
	return true
}

// validateSelectorResults 校验请求集合、终端事件与 selector 时间区间一一对应。
func (observation compiledSelectorBatchObservation) validateSelectorResults(specs map[GateID]compiledSelectorBatchSpec) error {
	if len(observation.extraTopLevel) != 0 {
		return fmt.Errorf("compiled selector batch emitted unexpected top-level tests: %s", strings.Join(observation.extraTopLevel, ","))
	}
	for _, spec := range specs {
		matched := exactGoTestTimings(observation.selectorTimings[spec.name], spec.name)
		if len(matched) != 1 {
			return fmt.Errorf("compiled selector batch test %q has %d terminal results", spec.name, len(matched))
		}
		interval, ok := observation.selectorIntervals[spec.name]
		if !ok || !interval.completedAt.After(interval.runAt) {
			return fmt.Errorf("compiled selector batch test %q has an invalid run interval", spec.name)
		}
	}
	return nil
}

type compiledSelectorBatchJSONEvent struct {
	Action  string  `json:"Action"`
	Time    string  `json:"Time"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

func parseCompiledSelectorBatchEventTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("batched go test event time is required")
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse batched go test event time: %w", err)
	}
	return parsed.UTC(), nil
}

type compiledSelectorBatchEventWriter struct {
	destination       *boundedPlanLog
	expected          map[string]struct{}
	pending           []byte
	selectorTimings   map[string][]GoTestTiming
	selectorIntervals map[string]compiledSelectorBatchInterval
	selectorLogs      map[string]*boundedPlanLog
	seenRuns          map[string]struct{}
	seenTopLevel      map[string]struct{}
	extraTopLevel     []string
	terminalErr       error
}

func newCompiledSelectorBatchEventWriter(destination *boundedPlanLog, expected map[string]struct{}) *compiledSelectorBatchEventWriter {
	selectorLogs := make(map[string]*boundedPlanLog, len(expected))
	for name := range expected {
		selectorLogs[name] = newBoundedPlanLog(executorPlanMaxLogBytes)
	}
	return &compiledSelectorBatchEventWriter{
		destination: destination, expected: expected, selectorTimings: make(map[string][]GoTestTiming, len(expected)),
		selectorIntervals: make(map[string]compiledSelectorBatchInterval, len(expected)), selectorLogs: selectorLogs,
		seenTopLevel: make(map[string]struct{}, len(expected)), seenRuns: make(map[string]struct{}, len(expected)),
	}
}

// Write 消费 test2json 的换行事件，并按顶层 selector 归档 timing/log。
func (writer *compiledSelectorBatchEventWriter) Write(data []byte) (int, error) {
	if writer.terminalErr != nil {
		return 0, writer.terminalErr
	}
	writer.pending = append(writer.pending, data...)
	for {
		end := bytes.IndexByte(writer.pending, '\n')
		if end < 0 {
			return len(data), nil
		}
		line := append([]byte(nil), writer.pending[:end+1]...)
		writer.pending = append(writer.pending[:0], writer.pending[end+1:]...)
		if err := writer.consumeLine(line); err != nil {
			writer.terminalErr = err
			return 0, err
		}
	}
}

// consumeLine 将单行 test2json 事件交给结构化解析器或普通日志。
func (writer *compiledSelectorBatchEventWriter) consumeLine(line []byte) error {
	trimmed := bytes.TrimSuffix(line, []byte{'\n'})
	if !bytes.HasPrefix(trimmed, []byte{'{'}) {
		_, err := writer.destination.Write(line)
		return err
	}
	var event compiledSelectorBatchJSONEvent
	if err := json.Unmarshal(trimmed, &event); err != nil {
		return fmt.Errorf("decode batched go test event: %w", err)
	}
	eventTime, err := parseCompiledSelectorBatchEventTime(event.Time)
	if err != nil {
		return err
	}
	return writer.consumeEvent(event, eventTime)
}

// consumeEvent 分派普通输出和终端测试事件，保持单行解析的低复杂度。
func (writer *compiledSelectorBatchEventWriter) consumeEvent(event compiledSelectorBatchJSONEvent, eventTime time.Time) error {
	topLevel := topLevelCompiledSelectorName(event.Test)
	selectorLog := writer.selectorLogs[topLevel]
	if event.Output != "" {
		if err := writer.writeEventOutput(event.Output, selectorLog); err != nil {
			return err
		}
	}
	if event.Test != "" && event.Test == topLevel {
		if err := writer.consumeTopLevelLifecycleEvent(event.Action, topLevel, eventTime); err != nil {
			return err
		}
	}
	if event.Test == "" || !isCompiledSelectorTerminalStatus(event.Action) {
		return nil
	}
	return writer.consumeTerminalEvent(event, topLevel, selectorLog, eventTime)
}

// consumeTopLevelLifecycleEvent 以 cont 事件替换 parallel selector 的正文起点，
// 避免把 pause 队列等待错误计入测试正文耗时。
func (writer *compiledSelectorBatchEventWriter) consumeTopLevelLifecycleEvent(action string, topLevel string, eventTime time.Time) error {
	switch action {
	case "run":
		return writer.consumeTopLevelRunEvent(topLevel, eventTime)
	case "pause":
		return writer.consumeTopLevelPauseEvent(topLevel, eventTime)
	case "cont":
		return writer.consumeTopLevelContinueEvent(topLevel, eventTime)
	default:
		return nil
	}
}

// consumeTopLevelRunEvent 建立 selector 的初始生命周期区间。
func (writer *compiledSelectorBatchEventWriter) consumeTopLevelRunEvent(topLevel string, eventTime time.Time) error {
	if _, expected := writer.expected[topLevel]; !expected {
		writer.extraTopLevel = appendUniqueString(writer.extraTopLevel, topLevel)
		return nil
	}
	if _, duplicate := writer.seenRuns[topLevel]; duplicate {
		return fmt.Errorf("batched go test selector %q has duplicate run events", topLevel)
	}
	writer.seenRuns[topLevel] = struct{}{}
	writer.selectorIntervals[topLevel] = compiledSelectorBatchInterval{runAt: eventTime}
	return nil
}

// consumeTopLevelPauseEvent 记录 parallel selector 已离开可运行队列。
func (writer *compiledSelectorBatchEventWriter) consumeTopLevelPauseEvent(topLevel string, eventTime time.Time) error {
	if _, expected := writer.expected[topLevel]; !expected {
		return nil
	}
	interval, ok := writer.selectorIntervals[topLevel]
	if !ok {
		return fmt.Errorf("batched go test selector %q has pause without run", topLevel)
	}
	if interval.paused || interval.continued || !eventTime.After(interval.runAt) {
		return fmt.Errorf("batched go test selector %q has an invalid pause event", topLevel)
	}
	interval.paused = true
	writer.selectorIntervals[topLevel] = interval
	return nil
}

// consumeTopLevelContinueEvent 以 parallel selector 恢复执行的时刻作为正文起点。
func (writer *compiledSelectorBatchEventWriter) consumeTopLevelContinueEvent(topLevel string, eventTime time.Time) error {
	if _, expected := writer.expected[topLevel]; !expected {
		return nil
	}
	interval, ok := writer.selectorIntervals[topLevel]
	if !ok {
		return fmt.Errorf("batched go test selector %q has cont without run", topLevel)
	}
	if !interval.paused || interval.continued || !eventTime.After(interval.runAt) {
		return fmt.Errorf("batched go test selector %q has an invalid cont event", topLevel)
	}
	interval.runAt, interval.continued = eventTime, true
	writer.selectorIntervals[topLevel] = interval
	return nil
}

// consumeTerminalEvent 校验并归档一个顶层或子测试终态。
func (writer *compiledSelectorBatchEventWriter) consumeTerminalEvent(event compiledSelectorBatchJSONEvent, topLevel string, selectorLog *boundedPlanLog, eventTime time.Time) error {
	timing, err := compiledSelectorBatchTiming(event)
	if err != nil {
		return err
	}
	if _, expected := writer.expected[topLevel]; !expected {
		writer.extraTopLevel = appendUniqueString(writer.extraTopLevel, topLevel)
		return nil
	}
	if event.Test == topLevel {
		if err := writer.consumeTopLevelTerminalInterval(topLevel, eventTime); err != nil {
			return err
		}
	}
	writer.selectorTimings[topLevel] = append(writer.selectorTimings[topLevel], timing)
	if selectorLog != nil {
		if err := writeBatchTiming(selectorLog, timing); err != nil {
			return err
		}
	}
	return writeBatchTiming(writer.destination, timing)
}

// consumeTopLevelTerminalInterval 完成顶层 selector 的精确活动区间。
func (writer *compiledSelectorBatchEventWriter) consumeTopLevelTerminalInterval(topLevel string, eventTime time.Time) error {
	interval, ok := writer.selectorIntervals[topLevel]
	if !ok {
		return fmt.Errorf("batched go test selector %q has terminal event without run event", topLevel)
	}
	if _, duplicate := writer.seenTopLevel[topLevel]; duplicate {
		return fmt.Errorf("batched go test timing %q has duplicate terminal events", topLevel)
	}
	if interval.paused && !interval.continued {
		return fmt.Errorf("batched go test selector %q terminated while paused", topLevel)
	}
	if !eventTime.After(interval.runAt) {
		return fmt.Errorf("batched go test selector %q has a non-positive run interval", topLevel)
	}
	writer.seenTopLevel[topLevel] = struct{}{}
	interval.completedAt = eventTime
	writer.selectorIntervals[topLevel] = interval
	return nil
}

// writeEventOutput 同时保留聚合诊断和 selector 专属日志。
func (writer *compiledSelectorBatchEventWriter) writeEventOutput(output string, selectorLog *boundedPlanLog) error {
	if _, err := io.WriteString(writer.destination, output); err != nil {
		return err
	}
	if selectorLog == nil {
		return nil
	}
	_, err := io.WriteString(selectorLog, output)
	return err
}

// writeBatchTiming 把终端测试事件写成既有机器可读日志记录。
func writeBatchTiming(destination io.Writer, timing GoTestTiming) error {
	_, err := fmt.Fprintf(destination, "%sname=%s status=%s duration_ms=%d\n", testtiming.LogPrefix, timing.Name, timing.Status, timing.DurationMS)
	return err
}

// Close 拒绝批量 test2json 流末尾未闭合的 JSON 事件。
func (writer *compiledSelectorBatchEventWriter) Close() error {
	if writer.terminalErr != nil {
		return writer.terminalErr
	}
	if len(writer.pending) == 0 {
		return nil
	}
	if bytes.HasPrefix(writer.pending, []byte{'{'}) {
		return errors.New("batched go test timing stream ended with an incomplete JSON event")
	}
	_, err := writer.destination.Write(writer.pending)
	writer.pending = nil
	return err
}

func topLevelCompiledSelectorName(name string) string {
	if topLevel, _, found := strings.Cut(name, "/"); found {
		return topLevel
	}
	return name
}

func isCompiledSelectorTerminalStatus(value string) bool {
	switch GoTestStatus(value) {
	case GoTestStatusPass, GoTestStatusFail, GoTestStatusSkip:
		return true
	default:
		return false
	}
}

// compiledSelectorBatchTiming 将 test2json 事件转换为有界毫秒终态。
func compiledSelectorBatchTiming(event compiledSelectorBatchJSONEvent) (GoTestTiming, error) {
	duration := event.Elapsed * 1000
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > float64(math.MaxInt64) {
		return GoTestTiming{}, errors.New("batched go test timing duration is invalid")
	}
	timing := GoTestTiming{Name: event.Test, Status: GoTestStatus(event.Action), DurationMS: max(1, int64(math.Round(duration)))}
	if err := testtiming.Validate(timing); err != nil {
		return GoTestTiming{}, err
	}
	return timing, nil
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}

// runCompiledSelectorBatchProcess 为每个 batch 创建独立 HOME/TMP/XDG/GOTMPDIR
// 和 batch 运行时目录；同一 shard 的 candidate cache 增量层可写共享，
// accepted seed 只读共享，而 proxy metrics 仍按 batch 独立记录。
func runCompiledSelectorBatchProcess(ctx context.Context, artifact compiledGroupArtifact, batch CompileGroupBatch, argv []string, specs map[GateID]compiledSelectorBatchSpec, now func() time.Time) (observation compiledSelectorBatchObservation) {
	started := now().UTC()
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	packageDir, packageErr := trustedCompileGroupPackageDirectory(artifact.layout.sourceCopy, artifact.packageDir)
	if packageErr != nil {
		return compiledSelectorBatchObservation{started: started, completed: now().UTC(), log: log, runErr: packageErr, contextErr: ctx.Err()}
	}
	expected := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		expected[spec.name] = struct{}{}
	}
	batchEnvironment, batchRoot, shortTempRoot, environmentErr := compileGroupBatchEnvironment(artifact, batch.BatchID)
	if environmentErr != nil {
		return compiledSelectorBatchObservation{started: started, completed: now().UTC(), log: log, runErr: environmentErr, contextErr: ctx.Err()}
	}
	batchEnvironment = compileGroupBatchProcessEnvironment(batchEnvironment, artifact.group.PackageTarget)
	defer func() {
		if cleanupErr := cleanupCompileGroupBatchRoots(batchRoot, shortTempRoot); cleanupErr != nil {
			cleanupObservationErr := errors.Join(errCompiledSelectorBatchCleanup, fmt.Errorf("remove compile group batch runtime roots: %w", cleanupErr))
			// 清理失败属于 batch 级执行错误；只绑定一次，避免同一错误在
			// runErr/closeErr 两条来源重复计入 execution outcome。
			observation.runErr = errors.Join(observation.runErr, cleanupObservationErr)
		}
	}()
	eventWriter := newCompiledSelectorBatchEventWriter(log, expected)
	command := exec.CommandContext(ctx, artifact.goBinary, argv[1:]...)
	configureCommandCancellation(command)
	command.Args[0], command.Dir, command.Env = argv[0], packageDir, batchEnvironment
	command.Stdout, command.Stderr = eventWriter, log
	var bodyStarted time.Time
	runErr := runConfiguredCommandWithStart(command, func() {
		bodyStarted = now().UTC()
	})
	closeErr := eventWriter.Close()
	selectorLogs := make(map[string][]byte, len(eventWriter.selectorLogs))
	for name, selectorLog := range eventWriter.selectorLogs {
		selectorLogs[name] = selectorLog.Bytes()
	}
	observation = compiledSelectorBatchObservation{
		started: started, bodyStarted: bodyStarted, completed: now().UTC(),
		selectorTimings: eventWriter.selectorTimings, selectorIntervals: eventWriter.selectorIntervals, selectorLogs: selectorLogs, log: log,
		runErr: runErr, closeErr: closeErr, parseErr: eventWriter.terminalErr,
		contextErr: ctx.Err(), extraTopLevel: eventWriter.extraTopLevel,
	}
	return observation
}

// configureCompileGroupBatchCache 让同一 shard 的所有 batch 共享 candidate 增量 cache，
// 只读消费 accepted baseline seed，并各自写 metrics。
func configureCompileGroupBatchCache(environment *[]string, artifact compiledGroupArtifact, batchID string) error {
	if artifact.candidateCacheRoot == "" || artifact.baselineCacheSeedRoot == "" {
		if len(artifact.group.BatchPlan) != 0 {
			return errors.New("compile group batch requires candidate and baseline cache roots")
		}
		return nil
	}
	if err := validateCompileGroupBatchCacheRoots(artifact); err != nil {
		return err
	}
	launcher, err := executorGoBuildCacheProxyLauncher()
	if err != nil {
		return err
	}
	proxyCommand, err := executorGoBuildCacheProxyCommand(launcher, artifact.baselineCacheSeedRoot, artifact.candidateCacheRoot)
	if err != nil {
		return err
	}
	invocation := "compile-group-" + strings.TrimPrefix(artifact.group.GroupID, "sha256:") + "-batch-" + batchID
	metricsPath, err := GoBuildCacheProxyMetricsPathForInvocation(artifact.candidateCacheRoot, invocation)
	if err != nil {
		return err
	}
	proxyCommand += " --metrics " + strconv.Quote(metricsPath)
	*environment = setCompileGroupEnvironmentValue(*environment, "GOCACHE", artifact.candidateCacheRoot)
	*environment = setCompileGroupEnvironmentValue(*environment, "GOCACHEPROG", proxyCommand)
	return nil
}

// validateCompileGroupBatchCacheRoots 校验生产 batch 使用的 shard 共享 candidate/seed 根目录。
func validateCompileGroupBatchCacheRoots(artifact compiledGroupArtifact) error {
	candidate, seed := artifact.candidateCacheRoot, artifact.baselineCacheSeedRoot
	if !filepath.IsAbs(candidate) || !filepath.IsAbs(seed) {
		return errors.New("compile group batch cache roots must be absolute")
	}
	for _, item := range []struct{ name, root string }{{name: "candidate", root: candidate}, {name: "baseline", root: seed}} {
		info, err := os.Stat(item.root)
		if err != nil {
			return fmt.Errorf("stat %s compile group cache root: %w", item.name, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s compile group cache root is not a directory", item.name)
		}
	}
	if filepath.Clean(candidate) == filepath.Clean(seed) || pathContains(candidate, seed) || pathContains(seed, candidate) {
		return errors.New("compile group candidate and baseline cache roots must be disjoint")
	}
	return nil
}

// compiledSelectorBatchResults 将每个顶层 selector 生成独立的 canonical 计划结果。
func compiledSelectorBatchResults(group CompileGroup, argv []string, specs map[GateID]compiledSelectorBatchSpec, observation compiledSelectorBatchObservation) (map[GateID]PlanGateExecution, error) {
	results := make(map[GateID]PlanGateExecution, len(specs))
	var resultErr error
	fullFailureLogAvailable := true
	for _, id := range group.WorkloadIDs {
		spec := specs[id]
		timings := observation.selectorTimings[spec.name]
		matched := exactGoTestTimings(timings, spec.name)
		if len(matched) != 1 {
			selectorErr := fmt.Errorf("compiled selector batch test %q has no unique terminal result", spec.name)
			cancelled, profileErr := cancelledCompiledSelectorResult(id, argv, observation, spec.name, fullFailureLogAvailable)
			if profileErr != nil {
				return results, errors.Join(resultErr, profileErr)
			}
			results[id] = cancelled
			fullFailureLogAvailable = false
			resultErr = errors.Join(resultErr, selectorErr)
			continue
		}
		interval := observation.selectorIntervals[spec.name]
		selectorErr := error(nil)
		if matched[0].Status == GoTestStatusFail {
			selectorErr = fmt.Errorf("compiled selector %q reported fail", spec.name)
		}
		fullFailureLog := selectorErr != nil && fullFailureLogAvailable
		result, profileErr := compiledSelectorResultWithLog(id, argv, observation, spec.name, timings, interval, selectorErr, fullFailureLog)
		if profileErr != nil {
			return results, errors.Join(resultErr, profileErr)
		}
		if fullFailureLog {
			fullFailureLogAvailable = false
		}
		if timingErr := validateCompiledSelectorTiming(id, timings); timingErr != nil {
			result.Status, result.ExitCode = ResultStatusFailed, ExecutorExitCode(timingErr)
			selectorErr = errors.Join(selectorErr, timingErr)
		}
		if result.Status == ResultStatusFailed {
			resultErr = errors.Join(resultErr, selectorErr, fmt.Errorf("compiled selector %q failed", id))
		}
		results[id] = result
	}
	return results, resultErr
}

// compiledSelectorResultWithLog 绑定 selector 专属日志并还原其独立启动、正文和总区间。
func compiledSelectorResultWithLog(id GateID, argv []string, observation compiledSelectorBatchObservation, selectorName string, timings []GoTestTiming, interval compiledSelectorBatchInterval, selectorErr error, fullDiagnostic bool) (PlanGateExecution, error) {
	log := newBoundedPlanLog(executorPlanMaxLogBytes)
	data := observation.selectorLogs[selectorName]
	if len(data) == 0 && observation.log != nil {
		data = observation.log.Bytes()
	}
	if len(data) != 0 {
		_, _ = log.Write(compiledSelectorDiagnosticLog(data, selectorErr, fullDiagnostic))
	}
	compiledObservation := compiledSelectorObservation{
		started: observation.started, bodyStarted: observation.bodyStarted, completed: observation.completed,
		log: log, timings: timings, argv: argv, runErr: selectorErr, contextErr: observation.contextErr,
	}
	if interval.runAt.IsZero() || interval.completedAt.IsZero() {
		return compiledSelectorResult(id, argv, compiledObservation)
	}
	started, bodyStarted, completed, intervalErr := canonicalCompiledSelectorBatchInterval(observation.started, interval)
	if intervalErr != nil {
		compiledObservation.runErr = errors.Join(compiledObservation.runErr, intervalErr)
	} else {
		completed = normalizeCompiledSelectorBatchCompletion(bodyStarted, completed, timings, selectorName)
		compiledObservation.started, compiledObservation.bodyStarted, compiledObservation.completed = started, bodyStarted, completed
	}
	result, profileErr := compiledSelectorResult(id, argv, compiledObservation)
	if profileErr != nil {
		return PlanGateExecution{}, profileErr
	}
	if intervalErr == nil {
		canonical, err := CanonicalizePlanGateExecutionTiming(result)
		if err != nil {
			return result, err
		}
		result = canonical
	}
	return result, nil
}

// normalizeCompiledSelectorBatchCompletion 以 exact top-level test2json timing
// 覆盖事件 Time 区间的量化误差，同时保留 cont 作为正文起点和原始 started。
func normalizeCompiledSelectorBatchCompletion(bodyStarted, completed time.Time, timings []GoTestTiming, selectorName string) time.Time {
	if bodyStarted.IsZero() || completed.IsZero() || selectorName == "" {
		return completed
	}
	matched := exactGoTestTimings(timings, selectorName)
	if len(matched) != 1 || matched[0].DurationMS <= 0 {
		return completed
	}
	// test2json 的 terminal duration 是 selector 正文的唯一权威耗时。
	// 事件完成时间可能包含终态之后的管道/清理尾部，不能把这段尾部
	// 投影进 TestBodyMS；同时也要向上覆盖量化误差导致的过短区间。
	return bodyStarted.Add(time.Duration(matched[0].DurationMS) * time.Millisecond)
}

// compiledSelectorDiagnosticLog 对成功 selector 只保留有界尾部，避免数百个
// PASS 日志挤占 1 MiB 结构化回执；失败 selector 仍保留完整诊断窗口。
func compiledSelectorDiagnosticLog(data []byte, selectorErr error, fullDiagnostic bool) []byte {
	if (selectorErr != nil && fullDiagnostic) || len(data) <= executorPlanSuccessfulSelectorLogBytes {
		return data
	}
	log := newBoundedPlanLog(executorPlanSuccessfulSelectorLogBytes)
	_, _ = log.Write(data)
	return log.Bytes()
}

// canonicalCompiledSelectorBatchInterval 以真实进程启动、selector run 和终态 Time 还原三段区间。
func canonicalCompiledSelectorBatchInterval(processStarted time.Time, interval compiledSelectorBatchInterval) (time.Time, time.Time, time.Time, error) {
	if processStarted.IsZero() || interval.runAt.IsZero() || interval.completedAt.IsZero() || !interval.runAt.After(processStarted) || !interval.completedAt.After(interval.runAt) {
		return time.Time{}, time.Time{}, time.Time{}, errors.New("compiled selector batch event interval is invalid")
	}
	processStarted = processStarted.UTC().Truncate(cicontract.TimingResolution)
	bodyStarted := interval.runAt.UTC().Truncate(cicontract.TimingResolution)
	completed := interval.completedAt.UTC().Truncate(cicontract.TimingResolution)
	if !bodyStarted.After(processStarted) {
		bodyStarted = processStarted.Add(cicontract.TimingResolution)
	}
	if !completed.After(bodyStarted) {
		completed = bodyStarted.Add(cicontract.TimingResolution)
	}
	started := bodyStarted.Add(-cicontract.TimingResolution)
	if started.Before(processStarted) {
		started = processStarted
	}
	if !bodyStarted.After(started) {
		started = bodyStarted.Add(-cicontract.TimingResolution)
	}
	return started, bodyStarted, completed, nil
}
