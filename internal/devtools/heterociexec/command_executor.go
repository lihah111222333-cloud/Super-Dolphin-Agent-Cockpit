package heterociexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

// CommandFactory 将已校验的 workflow batch 转换为一个不可变部署命令。
type CommandFactory func(batch heterociworkflow.ExecutionBatch) (Command, error)

// RunOutcome 是 runner 返回的明确进程退出码和可选 per-unit 终态集合。
type RunOutcome struct {
	ExitCode int
	Outcomes []heterociworkflow.BatchOutcome
}

// Runner 是不执行 fallback 的注入式命令运行端口。
type Runner interface {
	Run(ctx context.Context, command Command) (RunOutcome, error)
}

// RunnerFunc 将函数适配为 Runner，便于测试和受控宿主接线。
type RunnerFunc func(ctx context.Context, command Command) (RunOutcome, error)

// Run 调用注入的 runner 函数；nil 函数由构造器拒绝。
func (function RunnerFunc) Run(ctx context.Context, command Command) (RunOutcome, error) {
	if function == nil {
		return RunOutcome{}, ErrCommandExecutor
	}
	return function(ctx, command)
}

// ReceiptStore 提供同一个 batch identity 的严格 receipt 读写端口。
type ReceiptStore interface {
	Claim(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (bool, error)
	Read(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error)
	WriteIfAbsent(ctx context.Context, result heterociworkflow.BatchResult) error
}

// CommandBatchExecutor 将 command runner 接线到 workflow BatchExecutor 和 BatchReceiptReader。
type CommandBatchExecutor struct {
	factory  CommandFactory
	runner   Runner
	receipts ReceiptStore

	mu         sync.Mutex
	executions map[commandExecutionKey]*commandExecution
}

// ErrCommandExecutor 表示 command executor 的依赖、batch 或结果契约不合法。
var ErrCommandExecutor = errors.New("heteroci command executor is invalid")

// ErrReceiptRead 表示 durable receipt 读取失败，不能被猜测或吞掉。
var ErrReceiptRead = errors.New("heteroci command executor receipt read failed")

// ErrReceiptClaim 表示 durable receipt claim 失败，不能猜测 owner 或重执行。
var ErrReceiptClaim = errors.New("heteroci command executor receipt claim failed")

// ErrReceiptWrite 表示 durable receipt 写入失败，不能被当作执行成功。
var ErrReceiptWrite = errors.New("heteroci command executor receipt write failed")

// ErrCommandInFlight 表示同一 operation/batch 已被其他执行路径占用，禁止等待或再次运行。
var ErrCommandInFlight = errors.New("heteroci command batch is already in flight")

type commandExecution struct {
	done          chan struct{}
	commandDigest string
	result        heterociworkflow.BatchResult
	err           error
}

type commandExecutionKey struct {
	batchID        heterociworkflow.BatchID
	materialDigest string
}

// NewCommandBatchExecutor 构造显式 factory、runner 和 receipt store 的一次性 executor。
func NewCommandBatchExecutor(factory CommandFactory, runner Runner, receipts ReceiptStore) (*CommandBatchExecutor, error) {
	if factory == nil || isNilInterface(runner) || isNilInterface(receipts) {
		return nil, ErrCommandExecutor
	}
	return &CommandBatchExecutor{factory: factory, runner: runner, receipts: receipts, executions: make(map[commandExecutionKey]*commandExecution)}, nil
}

// ExecuteBatch 先原子占用本地 batch，再进行 receipt I/O，避免第二调用被阻塞 read 拖住。
func (executor *CommandBatchExecutor) ExecuteBatch(ctx context.Context, batch heterociworkflow.ExecutionBatch) (heterociworkflow.BatchResult, error) {
	batch = cloneCommandExecutionBatch(batch)
	if err := executor.validateContextAndBatch(ctx, batch); err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	command, commandDigest, err := executor.prepareCommand(batch)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	key := commandExecutionKey{batchID: batch.BatchID, materialDigest: batch.MaterialDigest}
	record, owner, inFlight, err := executor.claim(key, commandDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	if inFlight {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: batch %s", ErrCommandInFlight, batch.BatchID)
	}
	if !owner {
		return executor.replayCompletedExecution(ctx, batch, commandDigest, record)
	}
	receipt, found, err := executor.readValidatedReceipt(ctx, batch, commandDigest)
	if err != nil {
		executor.complete(record, heterociworkflow.BatchResult{}, err)
		return heterociworkflow.BatchResult{}, err
	}
	if found {
		executor.complete(record, receipt, nil)
		return receipt, nil
	}
	result, executeErr := executor.executeClaimed(ctx, batch, command, commandDigest)
	executor.complete(record, result, executeErr)
	return cloneBatchResult(result), executeErr
}

// replayCompletedExecution 允许已完成调用读取 exact receipt，但绝不重新 Claim 或 runner。
func (executor *CommandBatchExecutor) replayCompletedExecution(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string, record *commandExecution) (heterociworkflow.BatchResult, error) {
	receipt, found, err := executor.readValidatedReceipt(ctx, batch, commandDigest)
	if err != nil {
		if record.err != nil {
			return cloneBatchResult(record.result), errors.Join(ErrCommandInFlight, record.err)
		}
		return heterociworkflow.BatchResult{}, err
	}
	if found {
		return receipt, nil
	}
	if record.err != nil {
		return cloneBatchResult(record.result), errors.Join(ErrCommandInFlight, record.err)
	}
	return cloneBatchResult(record.result), nil
}

// ReadBatchReceipt 实现 workflow.BatchReceiptReader，且严格按完整 expected batch 派生 digest 读取。
func (executor *CommandBatchExecutor) ReadBatchReceipt(ctx context.Context, batch heterociworkflow.ExecutionBatch) (heterociworkflow.BatchResult, bool, error) {
	if executor == nil || isNilInterface(executor.receipts) {
		return heterociworkflow.BatchResult{}, false, ErrCommandExecutor
	}
	if ctx == nil {
		return heterociworkflow.BatchResult{}, false, fmt.Errorf("%w: context is nil", ErrCommandExecutor)
	}
	if err := executor.validateContextAndBatch(ctx, batch); err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	_, commandDigest, err := executor.prepareCommand(batch)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	key := commandExecutionKey{batchID: batch.BatchID, materialDigest: batch.MaterialDigest}
	if executionErr := executor.blockingReceiptError(key); executionErr != nil {
		return heterociworkflow.BatchResult{}, false, executionErr
	}
	return executor.readValidatedReceipt(ctx, batch, commandDigest)
}

// blockingReceiptError 防止明确无效 factory/command/outcome 被后续 receipt 隐藏。
func (executor *CommandBatchExecutor) blockingReceiptError(key commandExecutionKey) error {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	record, exists := executor.executions[key]
	if !exists {
		return nil
	}
	select {
	case <-record.done:
		if record.err == nil || errors.Is(record.err, heterociworkflow.ErrBatchReceiptRequired) || errors.Is(record.err, ErrReceiptRead) {
			return nil
		}
		return record.err
	default:
		return nil
	}
}

// validateContextAndBatch 在任何 factory、runner 或 receipt side effect 前锁定 batch identity。
func (executor *CommandBatchExecutor) validateContextAndBatch(ctx context.Context, batch heterociworkflow.ExecutionBatch) error {
	if executor == nil || executor.factory == nil || isNilInterface(executor.runner) || isNilInterface(executor.receipts) {
		return ErrCommandExecutor
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrCommandExecutor)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: context: %w", ErrCommandExecutor, err)
	}
	_, err := validateExecutionBatch(batch)
	return err
}

// prepareCommand 在 durable Claim 前生成并校验唯一 canonical command 及其摘要。
func (executor *CommandBatchExecutor) prepareCommand(batch heterociworkflow.ExecutionBatch) (Command, string, error) {
	platform, err := validateExecutionBatch(batch)
	if err != nil {
		return Command{}, "", err
	}
	command, err := executor.factory(cloneCommandExecutionBatch(batch))
	if err != nil {
		return Command{}, "", fmt.Errorf("%w: command factory: %w", ErrCommandExecutor, err)
	}
	if err := validateCommandForBatch(platform, command); err != nil {
		return Command{}, "", err
	}
	digest, err := DeriveCommandDigest(command)
	if err != nil {
		return Command{}, "", err
	}
	return cloneCommand(command), digest, nil
}

// claim 原子建立或观察 BatchID 的本地执行记录；active 记录立即冲突，completed 记录只允许 replay。
func (executor *CommandBatchExecutor) claim(key commandExecutionKey, commandDigest string) (*commandExecution, bool, bool, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.executions == nil {
		return nil, false, false, ErrCommandExecutor
	}
	if record, exists := executor.executions[key]; exists {
		if record.commandDigest != commandDigest {
			return nil, false, false, fmt.Errorf("%w: command digest drifted for in-flight batch", ErrCommandExecutor)
		}
		select {
		case <-record.done:
			return record, false, false, nil
		default:
			return record, false, true, nil
		}
	}
	record := &commandExecution{done: make(chan struct{}), commandDigest: commandDigest}
	executor.executions[key] = record
	return record, true, false, nil
}

// complete 发布一次执行结果供同一实例的诊断读取。
func (executor *CommandBatchExecutor) complete(record *commandExecution, result heterociworkflow.BatchResult, err error) {
	executor.mu.Lock()
	record.result = cloneBatchResult(result)
	record.err = err
	close(record.done)
	executor.mu.Unlock()
}

// executeClaimed 执行一次 durable Claim→runner→receipt write 链，任何错误均停止该链。
func (executor *CommandBatchExecutor) executeClaimed(ctx context.Context, batch heterociworkflow.ExecutionBatch, command Command, commandDigest string) (heterociworkflow.BatchResult, error) {
	claimed, err := executor.receipts.Claim(ctx, batch, commandDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: %w", ErrReceiptClaim, err)
	}
	if !claimed {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: durable claim already exists for batch %s", ErrCommandInFlight, batch.BatchID)
	}
	if err := ctx.Err(); err != nil {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: before runner: %w", ErrCommandExecutor, err)
	}
	outcome, err := executor.runner.Run(ctx, cloneCommand(command))
	if err != nil {
		return executor.resolveRunnerError(ctx, batch, commandDigest, err)
	}
	result, err := resultFromRunOutcome(batch, commandDigest, outcome)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	if err := executor.receipts.WriteIfAbsent(ctx, cloneBatchResult(result)); err != nil {
		return executor.resolveWriteError(ctx, batch, commandDigest, result, err)
	}
	return result, nil
}

// resolveRunnerError 只查询同一材料和 command digest receipt，缺失时返回 durable receipt required。
func (executor *CommandBatchExecutor) resolveRunnerError(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string, cause error) (heterociworkflow.BatchResult, error) {
	receipt, found, readErr := executor.readValidatedReceipt(ctx, batch, commandDigest)
	if readErr != nil {
		return heterociworkflow.BatchResult{}, errors.Join(heterociworkflow.ErrBatchReceiptRequired, cause, readErr)
	}
	if !found {
		return heterociworkflow.BatchResult{}, errors.Join(heterociworkflow.ErrBatchReceiptRequired, cause)
	}
	return cloneBatchResult(receipt), nil
}

// resolveWriteError 在 WriteIfAbsent ack 不确定时只回读同一材料和 command digest，禁止再次执行 runner。
func (executor *CommandBatchExecutor) resolveWriteError(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string, expected heterociworkflow.BatchResult, cause error) (heterociworkflow.BatchResult, error) {
	receipt, found, readErr := executor.readValidatedReceipt(ctx, batch, commandDigest)
	if readErr != nil {
		return heterociworkflow.BatchResult{}, errors.Join(ErrReceiptWrite, heterociworkflow.ErrBatchReceiptRequired, cause, readErr)
	}
	if !found {
		return heterociworkflow.BatchResult{}, errors.Join(ErrReceiptWrite, heterociworkflow.ErrBatchReceiptRequired, cause)
	}
	if !sameBatchResult(receipt, expected) {
		return heterociworkflow.BatchResult{}, errors.Join(ErrReceiptWrite, heterociworkflow.ErrBatchResult, cause)
	}
	return receipt, nil
}

// readValidatedReceipt 从 store 只读当前完整 batch 与 command digest，并校验完整结果。
func (executor *CommandBatchExecutor) readValidatedReceipt(ctx context.Context, batch heterociworkflow.ExecutionBatch, commandDigest string) (heterociworkflow.BatchResult, bool, error) {
	receipt, found, err := executor.receipts.Read(ctx, batch, commandDigest)
	if err != nil {
		return heterociworkflow.BatchResult{}, false, fmt.Errorf("%w: %w", ErrReceiptRead, err)
	}
	if !found {
		return heterociworkflow.BatchResult{}, false, nil
	}
	if err := validateBatchResult(batch, commandDigest, receipt); err != nil {
		return heterociworkflow.BatchResult{}, false, err
	}
	receipt = reorderBatchResult(batch, receipt)
	return cloneBatchResult(receipt), true, nil
}

// validateExecutionBatch 校验 batch key、unit、assignment、wave 和确定性 BatchID。
func validateExecutionBatch(batch heterociworkflow.ExecutionBatch) (heteroci.Platform, error) {
	if err := validateExecutionBatchBasics(batch); err != nil {
		return "", err
	}
	platform, err := validateBatchShape(batch)
	if err != nil {
		return "", err
	}
	if err := validateBatchAssignments(batch); err != nil {
		return "", err
	}
	if err := validateExecutionBatchMaterial(batch); err != nil {
		return "", err
	}
	return platform, nil
}

// validateExecutionBatchBasics 校验 batch identity、executor key 和 execution units 基础形状。
func validateExecutionBatchBasics(batch heterociworkflow.ExecutionBatch) error {
	if !validCommandBatchID(batch.BatchID) {
		return fmt.Errorf("%w: batch id is invalid", ErrCommandExecutor)
	}
	if err := batch.Key.Validate(); err != nil {
		return fmt.Errorf("%w: executor key: %w", ErrCommandExecutor, err)
	}
	if err := heteroci.ValidateExecutionUnits(batch.Units); err != nil {
		return fmt.Errorf("%w: units: %w", ErrCommandExecutor, err)
	}
	return nil
}

// validateExecutionBatchMaterial 重算完整材料摘要并锁定 canonical BatchID。
func validateExecutionBatchMaterial(batch heterociworkflow.ExecutionBatch) error {
	if batch.MaterialDigest == "" {
		return fmt.Errorf("%w: material digest is missing", ErrCommandExecutor)
	}
	wantMaterialDigest, err := heterociworkflow.DeriveExecutionMaterialDigest(batch)
	if err != nil || wantMaterialDigest != batch.MaterialDigest {
		return fmt.Errorf("%w: material digest does not match canonical batch", ErrCommandExecutor)
	}
	unitIDs := make([]string, len(batch.Units))
	for index, unit := range batch.Units {
		unitIDs[index] = unit.UnitID
	}
	wantID, err := heterociworkflow.DeriveBatchID(batch.RunID, batch.Key, batch.Wave, unitIDs)
	if err != nil || wantID != batch.BatchID {
		return fmt.Errorf("%w: batch identity does not match canonical derivation", ErrCommandExecutor)
	}
	return nil
}

// validateBatchShape 校验 wave 语义以及单 execution lane 的唯一目标平台。
func validateBatchShape(batch heterociworkflow.ExecutionBatch) (heteroci.Platform, error) {
	if batch.Wave < 0 {
		return "", fmt.Errorf("%w: local batch wave must be non-negative", ErrCommandExecutor)
	}
	platform := batch.Units[0].TargetPlatform
	for _, unit := range batch.Units[1:] {
		if unit.TargetPlatform != platform {
			return "", fmt.Errorf("%w: batch units target mixed platforms", ErrCommandExecutor)
		}
	}
	return platform, nil
}

// validateBatchAssignments 将每个 assignment 绑定到精确 unit 顺序和 executor key。
func validateBatchAssignments(batch heterociworkflow.ExecutionBatch) error {
	if len(batch.Assignments) != len(batch.Units) {
		return fmt.Errorf("%w: assignment count does not match units", ErrCommandExecutor)
	}
	seen := make(map[string]struct{}, len(batch.Assignments))
	for index, assignment := range batch.Assignments {
		if assignment.UnitID != batch.Units[index].UnitID {
			return fmt.Errorf("%w: assignment order or unit identity drifted", ErrCommandExecutor)
		}
		if _, exists := seen[assignment.UnitID]; exists {
			return fmt.Errorf("%w: assignment is duplicated", ErrCommandExecutor)
		}
		seen[assignment.UnitID] = struct{}{}
		if err := validateAssignmentForKey(batch.Key, assignment); err != nil {
			return err
		}
	}
	return nil
}

// validateAssignmentForKey 防止 local node identity 跨 lane 泄漏。
func validateAssignmentForKey(key heterociworkflow.ExecutorKey, assignment heteroci.RouteAssignment) error {
	if assignment.Executor != heteroci.ExecutorKindLocalNode || key.Kind != heteroci.ExecutorKindLocalNode {
		return fmt.Errorf("%w: assignment executor kind drifted", ErrCommandExecutor)
	}
	if assignment.NodeID != key.NodeID || assignment.Endpoint == "" || assignment.ProbeIdentity == "" || assignment.ProbeBootID == "" {
		return fmt.Errorf("%w: local assignment identity drifted", ErrCommandExecutor)
	}
	return nil
}

// validateCommandForBatch 强制 canonical 平台、argv 边界和 Windows 编码字段。
func validateCommandForBatch(platform heteroci.Platform, command Command) error {
	if command.Platform != platform {
		return fmt.Errorf("%w: command platform %q does not match batch %q", ErrCommandExecutor, command.Platform, platform)
	}
	canonical, err := (StrictCommandEncoder{}).Encode(platform, command.Argv)
	if err != nil {
		return fmt.Errorf("%w: command encoding: %w", ErrCommandExecutor, err)
	}
	if !slices.Equal(command.Argv, canonical.Argv) || command.CommandLine != canonical.CommandLine || command.CommandBase64 != canonical.CommandBase64 {
		return fmt.Errorf("%w: command argv or encoded fields are not canonical", ErrCommandExecutor)
	}
	return nil
}

// DeriveCommandDigest 为 canonical command 的平台、argv 和编码字段派生稳定摘要。
func DeriveCommandDigest(command Command) (string, error) {
	canonical, err := (StrictCommandEncoder{}).Encode(command.Platform, command.Argv)
	if err != nil {
		return "", fmt.Errorf("%w: command encoding: %w", ErrCommandExecutor, err)
	}
	if !slices.Equal(command.Argv, canonical.Argv) || command.CommandLine != canonical.CommandLine || command.CommandBase64 != canonical.CommandBase64 {
		return "", fmt.Errorf("%w: command is not canonical", ErrCommandExecutor)
	}
	material := struct {
		Platform      heteroci.Platform `json:"platform"`
		Argv          []string          `json:"argv"`
		CommandLine   string            `json:"command_line"`
		CommandBase64 string            `json:"command_base64"`
	}{
		Platform:      command.Platform,
		Argv:          slices.Clone(command.Argv),
		CommandLine:   command.CommandLine,
		CommandBase64: command.CommandBase64,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("%w: command digest encoding: %w", ErrCommandExecutor, err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("heteroci-command/v1\x00"))
	_, _ = hash.Write(encoded)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// resultFromRunOutcome 仅把明确 runner exit/outcome 映射为本地非权威结果。
func resultFromRunOutcome(batch heterociworkflow.ExecutionBatch, commandDigest string, outcome RunOutcome) (heterociworkflow.BatchResult, error) {
	if outcome.ExitCode < 0 {
		return heterociworkflow.BatchResult{}, fmt.Errorf("%w: runner exit code is negative", ErrCommandExecutor)
	}
	states, err := normalizeRunOutcomes(batch, outcome)
	if err != nil {
		return heterociworkflow.BatchResult{}, err
	}
	result := newCommandBatchResult(batch, commandDigest, states)
	return result, nil
}

// normalizeRunOutcomes 校验明确 per-unit outcome，或把明确 exit code 映射到每个 unit。
func normalizeRunOutcomes(batch heterociworkflow.ExecutionBatch, outcome RunOutcome) ([]heterociworkflow.BatchOutcome, error) {
	if len(outcome.Outcomes) == 0 {
		return defaultRunOutcomes(batch, outcome.ExitCode), nil
	}
	byID, err := indexRunOutcomes(outcome.Outcomes)
	if err != nil {
		return nil, err
	}
	outcomes, failed, err := orderRunOutcomes(batch, byID)
	if err != nil {
		return nil, err
	}
	if err := validateRunOutcomeExit(outcome.ExitCode, failed, len(byID), len(batch.Units)); err != nil {
		return nil, err
	}
	return outcomes, nil
}

// defaultRunOutcomes 将明确 exit code 统一映射到 batch 全部 unit。
func defaultRunOutcomes(batch heterociworkflow.ExecutionBatch, exitCode int) []heterociworkflow.BatchOutcome {
	state := heteroci.LeaseStateFailed
	if exitCode == 0 {
		state = heteroci.LeaseStateSucceeded
	}
	outcomes := make([]heterociworkflow.BatchOutcome, len(batch.Units))
	for index, unit := range batch.Units {
		outcomes[index] = heterociworkflow.BatchOutcome{UnitID: unit.UnitID, State: state}
	}
	return outcomes
}

// indexRunOutcomes 建立 runner outcome 唯一索引并拒绝非 terminal state。
func indexRunOutcomes(values []heterociworkflow.BatchOutcome) (map[string]heterociworkflow.BatchOutcome, error) {
	byID := make(map[string]heterociworkflow.BatchOutcome, len(values))
	for _, value := range values {
		if value.State != heteroci.LeaseStateSucceeded && value.State != heteroci.LeaseStateFailed {
			return nil, fmt.Errorf("%w: runner outcome state is invalid", ErrCommandExecutor)
		}
		if _, exists := byID[value.UnitID]; exists {
			return nil, fmt.Errorf("%w: runner outcome is duplicated", ErrCommandExecutor)
		}
		byID[value.UnitID] = value
	}
	return byID, nil
}

// orderRunOutcomes 按 batch unit 顺序重排 outcome 并统计明确失败数。
func orderRunOutcomes(batch heterociworkflow.ExecutionBatch, byID map[string]heterociworkflow.BatchOutcome) ([]heterociworkflow.BatchOutcome, int, error) {
	outcomes := make([]heterociworkflow.BatchOutcome, len(batch.Units))
	failed := 0
	for index, unit := range batch.Units {
		value, exists := byID[unit.UnitID]
		if !exists {
			return nil, 0, fmt.Errorf("%w: runner outcome coverage is incomplete", ErrCommandExecutor)
		}
		if value.State == heteroci.LeaseStateFailed {
			failed++
		}
		outcomes[index] = value
	}
	return outcomes, failed, nil
}

// validateRunOutcomeExit 拒绝 exit code 与完整 per-unit outcomes 互相矛盾。
func validateRunOutcomeExit(exitCode, failed, outcomeCount, unitCount int) error {
	if outcomeCount != unitCount || (exitCode == 0 && failed != 0) || (exitCode != 0 && failed == 0) {
		return fmt.Errorf("%w: runner exit and outcomes disagree", ErrCommandExecutor)
	}
	return nil
}

// newCommandBatchResult 创建本适配器唯一允许发出的 authority/status 形状。
func newCommandBatchResult(batch heterociworkflow.ExecutionBatch, commandDigest string, outcomes []heterociworkflow.BatchOutcome) heterociworkflow.BatchResult {
	return heterociworkflow.BatchResult{BatchID: batch.BatchID, RunID: batch.RunID, Executor: batch.Key, MaterialDigest: batch.MaterialDigest, CommandDigest: commandDigest, Authority: heteroci.AuthorityLocalNonAuthoritative, Status: heterociworkflow.ResultStatusNotVerified, Outcomes: slices.Clone(outcomes)}
}

// validateBatchResult 将 receipt 绑定到精确 batch identity 和 outcome coverage。
func validateBatchResult(batch heterociworkflow.ExecutionBatch, commandDigest string, result heterociworkflow.BatchResult) error {
	if result.BatchID != batch.BatchID || result.RunID != batch.RunID || result.Executor != batch.Key {
		return fmt.Errorf("%w: receipt identity drifted", heterociworkflow.ErrBatchResult)
	}
	if result.MaterialDigest != batch.MaterialDigest || result.MaterialDigest == "" || result.CommandDigest != commandDigest {
		return fmt.Errorf("%w: receipt material or command digest drifted", heterociworkflow.ErrBatchResult)
	}
	if err := validateReceiptHeader(result); err != nil {
		return err
	}
	if len(result.Outcomes) != len(batch.Units) {
		return fmt.Errorf("%w: receipt outcome count drifted", heterociworkflow.ErrBatchResult)
	}
	seen, err := indexReceiptOutcomes(result.Outcomes)
	if err != nil {
		return err
	}
	return validateReceiptCoverage(batch.Units, seen)
}

// indexReceiptOutcomes 建立 receipt outcome 唯一索引并拒绝非 terminal state。
func indexReceiptOutcomes(outcomes []heterociworkflow.BatchOutcome) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.State != heteroci.LeaseStateSucceeded && outcome.State != heteroci.LeaseStateFailed {
			return nil, fmt.Errorf("%w: receipt outcome state is invalid", heterociworkflow.ErrBatchResult)
		}
		if _, exists := seen[outcome.UnitID]; exists {
			return nil, fmt.Errorf("%w: receipt outcome is duplicated", heterociworkflow.ErrBatchResult)
		}
		seen[outcome.UnitID] = struct{}{}
	}
	return seen, nil
}

// validateReceiptCoverage 确认 receipt 恰好覆盖 batch 的全部 unit identity。
func validateReceiptCoverage(units []heteroci.ExecutionUnit, seen map[string]struct{}) error {
	for _, unit := range units {
		if _, exists := seen[unit.UnitID]; !exists {
			return fmt.Errorf("%w: receipt outcome coverage is incomplete", heterociworkflow.ErrBatchResult)
		}
	}
	return nil
}

// reorderBatchResult 按 execution batch unit 顺序稳定重排 receipt outcomes。
func reorderBatchResult(batch heterociworkflow.ExecutionBatch, result heterociworkflow.BatchResult) heterociworkflow.BatchResult {
	byID := make(map[string]heterociworkflow.BatchOutcome, len(result.Outcomes))
	for _, outcome := range result.Outcomes {
		byID[outcome.UnitID] = outcome
	}
	reordered := make([]heterociworkflow.BatchOutcome, len(batch.Units))
	for index, unit := range batch.Units {
		reordered[index] = byID[unit.UnitID]
	}
	result.Outcomes = reordered
	return result
}

// sameBatchResult 判断 WriteIfAbsent ack-lost 回读是否与本次明确 execution result 完全一致。
func sameBatchResult(left, right heterociworkflow.BatchResult) bool {
	return left.BatchID == right.BatchID &&
		left.RunID == right.RunID &&
		left.Executor == right.Executor &&
		left.MaterialDigest == right.MaterialDigest &&
		left.CommandDigest == right.CommandDigest &&
		left.Authority == right.Authority &&
		left.Status == right.Status &&
		slices.Equal(left.Outcomes, right.Outcomes)
}

// validateReceiptHeader 强制固定的本地非权威结果 envelope。
func validateReceiptHeader(result heterociworkflow.BatchResult) error {
	if result.Authority != heteroci.AuthorityLocalNonAuthoritative || result.Status != heterociworkflow.ResultStatusNotVerified {
		return fmt.Errorf("%w: receipt authority or status is invalid", heterociworkflow.ErrBatchResult)
	}
	return nil
}

// validCommandBatchID 校验 DeriveBatchID 产生的 64 位小写十六进制 identity。
func validCommandBatchID(batchID heterociworkflow.BatchID) bool {
	if len(batchID) != 64 {
		return false
	}
	for _, character := range []byte(batchID) {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

// cloneCommand 隔离 runner 对 argv backing array 的意外修改。
func cloneCommand(command Command) Command {
	command.Argv = slices.Clone(command.Argv)
	return command
}

// cloneCommandExecutionBatch 隔离 factory 对 batch slices 的意外修改。
func cloneCommandExecutionBatch(batch heterociworkflow.ExecutionBatch) heterociworkflow.ExecutionBatch {
	batch.Units = slices.Clone(batch.Units)
	batch.Assignments = slices.Clone(batch.Assignments)
	batch.Started = slices.Clone(batch.Started)
	return batch
}

// cloneBatchResult 隔离调用方或 receipt store 对 outcomes backing array 的意外修改。
func cloneBatchResult(result heterociworkflow.BatchResult) heterociworkflow.BatchResult {
	result.Outcomes = slices.Clone(result.Outcomes)
	return result
}
