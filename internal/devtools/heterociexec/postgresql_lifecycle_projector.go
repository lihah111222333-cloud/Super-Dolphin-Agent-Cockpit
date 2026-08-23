package heterociexec

import (
	"fmt"
	"slices"
	"time"
)

const (
	postgresqlLifecycleCompletedCode = "ok"
	// PostgreSQLReceiptLifecycleStageCount 是 canonical lifecycle 的固定阶段数。
	PostgreSQLReceiptLifecycleStageCount   = 8
	defaultPostgreSQLLifecycleFailureStage = PostgreSQLReceiptEventStageExecute
	postgreSQLLifecycleSkippedCode         = "not_run"
)

// PostgreSQLLifecycleProjectionInput 是离线 projector 唯一需要的调用方事实。
// 它不包含连接、存储、网络或 executor callback，因此不能隐式取得 authority。
type PostgreSQLLifecycleProjectionInput struct {
	OperationID           string
	RequestDigest         string
	ManifestDigest        string
	PlanDigest            string
	ExecutorBindingDigest string
	NodeSetDigest         string
	AuthorityGeneration   int64
	StageTimes            []time.Time
	TerminalShards        []PostgreSQLTerminalShard
	Status                string
	CleanupState          string
	ExitCode              int
	ErrorCode             string
	CapacityCode          string

	// FailureStage 只在 failed 终态使用；空值表示 canonical execute failure。
	FailureStage PostgreSQLReceiptEventStage
}

// PostgreSQLLifecycleProjection 是离线 projector 的不可变形状：完整八事件和唯一 terminal receipt。
type PostgreSQLLifecycleProjection struct {
	Events  []PostgreSQLReceiptEvent
	Receipt PostgreSQLTerminalReceipt
}

// ProjectPostgreSQLLifecycle 在内存中构造 canonical 八阶段事件链和 terminal receipt。
// 该函数只校验和编码调用方已提供的事实，绝不执行 PG、网络、SSH、云或本地 authority I/O。
func ProjectPostgreSQLLifecycle(input PostgreSQLLifecycleProjectionInput) (PostgreSQLLifecycleProjection, error) {
	input = clonePostgreSQLLifecycleProjectionInput(input)
	if err := validatePostgreSQLLifecycleProjectionInput(input); err != nil {
		return PostgreSQLLifecycleProjection{}, err
	}
	events, err := buildPostgreSQLLifecycleEvents(input)
	if err != nil {
		return PostgreSQLLifecycleProjection{}, err
	}
	receipt := PostgreSQLTerminalReceipt{
		SchemaVersion:         PostgreSQLTerminalReceiptSchema,
		OperationID:           input.OperationID,
		RequestDigest:         input.RequestDigest,
		ManifestDigest:        input.ManifestDigest,
		PlanDigest:            input.PlanDigest,
		ExecutorBindingDigest: input.ExecutorBindingDigest,
		NodeSetDigest:         input.NodeSetDigest,
		AuthorityGeneration:   input.AuthorityGeneration,
		StartedAt:             input.StageTimes[0],
		FinishedAt:            input.StageTimes[len(input.StageTimes)-1],
		ExitCode:              input.ExitCode,
		CleanupState:          input.CleanupState,
		ErrorCode:             input.ErrorCode,
		CapacityCode:          input.CapacityCode,
		Status:                input.Status,
		Shards:                slices.Clone(input.TerminalShards),
		EventSeq:              int64(len(events)),
		EventDigest:           events[len(events)-1].EventDigest,
	}
	if err := receipt.Validate(); err != nil {
		return PostgreSQLLifecycleProjection{}, fmt.Errorf("%w: projected terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	_, receiptDigest, err := EncodePostgreSQLTerminalReceipt(receipt)
	if err != nil {
		return PostgreSQLLifecycleProjection{}, fmt.Errorf("%w: encode projected terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	receipt.ReceiptDigest = receiptDigest
	if err := validatePostgreSQLReceiptEventChain(events, receipt); err != nil {
		return PostgreSQLLifecycleProjection{}, fmt.Errorf("%w: projected lifecycle chain: %w", ErrPostgreSQLReceiptStore, err)
	}
	return PostgreSQLLifecycleProjection{Events: events, Receipt: receipt}, nil
}

func validatePostgreSQLLifecycleProjectionInput(input PostgreSQLLifecycleProjectionInput) error {
	validators := []func(PostgreSQLLifecycleProjectionInput) error{
		validatePostgreSQLLifecycleIdentity,
		validatePostgreSQLLifecycleAuthority,
		func(value PostgreSQLLifecycleProjectionInput) error {
			return validatePostgreSQLLifecycleStageTimes(value.StageTimes)
		},
		validatePostgreSQLLifecycleCleanup,
		validatePostgreSQLLifecycleOutcome,
		validatePostgreSQLLifecycleNodeSet,
		validatePostgreSQLLifecycleFailureStage,
	}
	for _, validate := range validators {
		if err := validate(input); err != nil {
			return err
		}
	}
	return nil
}

func validatePostgreSQLLifecycleIdentity(input PostgreSQLLifecycleProjectionInput) error {
	if !validTerminalOperationID(input.OperationID) || !validTerminalDigest(input.RequestDigest) || !validTerminalDigest(input.ManifestDigest) || !validTerminalDigest(input.PlanDigest) || !validTerminalDigest(input.ExecutorBindingDigest) || !validTerminalDigest(input.NodeSetDigest) {
		return fmt.Errorf("%w: lifecycle identity is incomplete", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLLifecycleAuthority(input PostgreSQLLifecycleProjectionInput) error {
	if input.AuthorityGeneration <= 0 {
		return fmt.Errorf("%w: lifecycle authority generation is invalid", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLLifecycleCleanup(input PostgreSQLLifecycleProjectionInput) error {
	if input.CleanupState != PostgreSQLReceiptCleanupVerified {
		return fmt.Errorf("%w: lifecycle cleanup is not verified", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLLifecycleNodeSet(input PostgreSQLLifecycleProjectionInput) error {
	wantNodeSetDigest, err := PostgreSQLTerminalNodeSetDigest(input.TerminalShards)
	if err != nil || wantNodeSetDigest != input.NodeSetDigest {
		return fmt.Errorf("%w: lifecycle node set digest is not exact", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLLifecycleFailureStage(input PostgreSQLLifecycleProjectionInput) error {
	if input.Status == PostgreSQLReceiptStatusFailed {
		if input.FailureStage != "" && input.FailureStage != PostgreSQLReceiptEventStageMaterialize && input.FailureStage != PostgreSQLReceiptEventStageExecute {
			return fmt.Errorf("%w: lifecycle failure stage is invalid", ErrPostgreSQLReceiptStore)
		}
		return nil
	}
	if input.FailureStage != "" {
		return fmt.Errorf("%w: lifecycle failure stage is only valid for failed status", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLLifecycleStageTimes(times []time.Time) error {
	if len(times) != PostgreSQLReceiptLifecycleStageCount {
		return fmt.Errorf("%w: lifecycle requires exactly eight stage times", ErrPostgreSQLReceiptStore)
	}
	for index, occurredAt := range times {
		if !validPostgreSQLReceiptTime(occurredAt) {
			return fmt.Errorf("%w: lifecycle stage time %d is invalid", ErrPostgreSQLReceiptStore, index+1)
		}
		if index > 0 && occurredAt.Before(times[index-1]) {
			return fmt.Errorf("%w: lifecycle stage time regressed at %d", ErrPostgreSQLReceiptStore, index+1)
		}
	}
	return nil
}

func validatePostgreSQLLifecycleOutcome(input PostgreSQLLifecycleProjectionInput) error {
	switch input.Status {
	case PostgreSQLReceiptStatusSucceeded:
		return validatePostgreSQLLifecycleSuccessOutcome(input)
	case PostgreSQLReceiptStatusFailed:
		return validatePostgreSQLLifecycleFailedOutcome(input)
	case PostgreSQLReceiptStatusBlocked:
		return validatePostgreSQLLifecycleBlockedOutcome(input)
	default:
		return fmt.Errorf("%w: lifecycle status is invalid", ErrPostgreSQLReceiptStore)
	}
}
func validatePostgreSQLLifecycleSuccessOutcome(input PostgreSQLLifecycleProjectionInput) error {
	if input.ExitCode != 0 || input.ErrorCode != "" || input.CapacityCode != "" || len(input.TerminalShards) == 0 {
		return fmt.Errorf("%w: succeeded lifecycle outcome is incomplete", ErrPostgreSQLReceiptStore)
	}
	return validatePostgreSQLLifecycleTerminalShards(input.TerminalShards)
}
func validatePostgreSQLLifecycleFailedOutcome(input PostgreSQLLifecycleProjectionInput) error {
	if input.ExitCode <= 0 || input.ExitCode > 255 || !validTerminalCode(input.ErrorCode) || input.ErrorCode == "" || input.CapacityCode != "" || len(input.TerminalShards) == 0 {
		return fmt.Errorf("%w: failed lifecycle outcome is incomplete", ErrPostgreSQLReceiptStore)
	}
	return validatePostgreSQLLifecycleTerminalShards(input.TerminalShards)
}
func validatePostgreSQLLifecycleBlockedOutcome(input PostgreSQLLifecycleProjectionInput) error {
	if input.ExitCode != -1 || input.ErrorCode != "" || !validTerminalCode(input.CapacityCode) || input.CapacityCode == "" || len(input.TerminalShards) != 0 {
		return fmt.Errorf("%w: blocked lifecycle outcome is incomplete", ErrPostgreSQLReceiptStore)
	}
	return validatePostgreSQLLifecycleTerminalShards(input.TerminalShards)
}

func buildPostgreSQLLifecycleEvents(input PostgreSQLLifecycleProjectionInput) ([]PostgreSQLReceiptEvent, error) {
	stages := PostgreSQLReceiptLifecycleStages()
	if len(stages) != PostgreSQLReceiptLifecycleStageCount {
		return nil, fmt.Errorf("%w: lifecycle stage registry is not eight stages", ErrPostgreSQLReceiptStore)
	}
	events := make([]PostgreSQLReceiptEvent, 0, len(stages))
	for index, stage := range stages {
		state, code := postgreSQLLifecycleEventOutcome(input, index)
		shards := []PostgreSQLReceiptEventShard{}
		if input.Status != PostgreSQLReceiptStatusBlocked && index >= 3 {
			shards = projectPostgreSQLReceiptEventShards(input.TerminalShards, state, code)
		}
		nodeSetDigest, err := postgreSQLReceiptEventNodeSetDigest(shards)
		if err != nil {
			return nil, fmt.Errorf("%w: project lifecycle node set at stage %d: %w", ErrPostgreSQLReceiptStore, index+1, err)
		}
		event := PostgreSQLReceiptEvent{
			SchemaVersion:         PostgreSQLReceiptEventSchema,
			OperationID:           input.OperationID,
			ManifestDigest:        input.ManifestDigest,
			PlanDigest:            input.PlanDigest,
			ExecutorBindingDigest: input.ExecutorBindingDigest,
			NodeSetDigest:         nodeSetDigest,
			AuthorityGeneration:   input.AuthorityGeneration,
			Seq:                   int64(index + 1),
			OccurredAt:            input.StageTimes[index],
			Stage:                 stage,
			State:                 state,
			Code:                  code,
			Shards:                shards,
		}
		_, digest, err := EncodePostgreSQLReceiptEvent(event)
		if err != nil {
			return nil, fmt.Errorf("%w: encode lifecycle event %d: %w", ErrPostgreSQLReceiptStore, index+1, err)
		}
		event.EventDigest = digest
		events = append(events, event)
	}
	return events, nil
}

func postgreSQLLifecycleEventOutcome(input PostgreSQLLifecycleProjectionInput, index int) (PostgreSQLReceiptEventState, string) {
	switch input.Status {
	case PostgreSQLReceiptStatusBlocked:
		return postgreSQLLifecycleBlockedEventOutcome(input, index)
	case PostgreSQLReceiptStatusSucceeded:
		return postgreSQLLifecycleSucceededEventOutcome(index)
	default:
		return postgreSQLLifecycleFailedEventOutcome(input, index)
	}
}

func postgreSQLLifecycleSucceededEventOutcome(index int) (PostgreSQLReceiptEventState, string) {
	if index == 7 {
		return PostgreSQLReceiptEventStateCompleted, ""
	}
	return PostgreSQLReceiptEventStateCompleted, postgresqlLifecycleCompletedCode
}

func postgreSQLLifecycleBlockedEventOutcome(input PostgreSQLLifecycleProjectionInput, index int) (PostgreSQLReceiptEventState, string) {
	if index == 2 || index == 7 {
		return PostgreSQLReceiptEventStateBlocked, input.CapacityCode
	}
	if index >= 3 {
		return PostgreSQLReceiptEventStateSkipped, input.CapacityCode
	}
	return PostgreSQLReceiptEventStateCompleted, postgresqlLifecycleCompletedCode
}

func postgreSQLLifecycleFailedEventOutcome(input PostgreSQLLifecycleProjectionInput, index int) (PostgreSQLReceiptEventState, string) {
	failureStage := input.FailureStage
	if failureStage == "" {
		failureStage = defaultPostgreSQLLifecycleFailureStage
	}
	failureIndex := postgreSQLReceiptStageOrder(failureStage)
	if index == 7 || index == failureIndex {
		return PostgreSQLReceiptEventStateFailed, input.ErrorCode
	}
	if index > failureIndex && index < 6 {
		return PostgreSQLReceiptEventStateSkipped, postgreSQLLifecycleSkippedCode
	}
	return PostgreSQLReceiptEventStateCompleted, postgresqlLifecycleCompletedCode
}

func projectPostgreSQLReceiptEventShards(terminal []PostgreSQLTerminalShard, state PostgreSQLReceiptEventState, code string) []PostgreSQLReceiptEventShard {
	shards := make([]PostgreSQLReceiptEventShard, len(terminal))
	for index, shard := range terminal {
		shards[index] = PostgreSQLReceiptEventShard{
			NodeID:         shard.NodeID,
			NodeIDDigest:   shard.NodeIDDigest,
			ExecutorKind:   shard.ExecutorKind,
			ShardDigest:    shard.ShardDigest,
			BatchSetDigest: shard.BatchSetDigest,
			UnitSetDigest:  shard.UnitSetDigest,
			State:          state,
			Code:           code,
		}
	}
	return shards
}

func validatePostgreSQLLifecycleTerminalShards(shards []PostgreSQLTerminalShard) error {
	if shards == nil {
		return fmt.Errorf("%w: terminal shard set is missing", ErrPostgreSQLReceiptStore)
	}
	for index, shard := range shards {
		if index > 0 && shards[index-1].NodeID >= shard.NodeID {
			return fmt.Errorf("%w: terminal shards are not sorted", ErrPostgreSQLReceiptStore)
		}
		if err := shard.validate(); err != nil {
			return fmt.Errorf("%w: terminal shard %d is invalid: %w", ErrPostgreSQLReceiptStore, index+1, err)
		}
	}
	return nil
}

func clonePostgreSQLLifecycleProjectionInput(input PostgreSQLLifecycleProjectionInput) PostgreSQLLifecycleProjectionInput {
	input.StageTimes = slices.Clone(input.StageTimes)
	input.TerminalShards = slices.Clone(input.TerminalShards)
	return input
}
