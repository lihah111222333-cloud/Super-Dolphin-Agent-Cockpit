package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// ErrCalibrationCheckpointConflict 表示 checkpoint 场景在读取后已被另一连接修改。
var ErrCalibrationCheckpointConflict = errors.New("calibration checkpoint conflict")

// CalibrationCheckpointScenarioRecord 是 remote calibration checkpoint 的不透明持久化载荷。
type CalibrationCheckpointScenarioRecord struct {
	Scenario   string
	Started    bool
	Completed  bool
	InputJSON  string
	ResultJSON string
}

// CalibrationCheckpointRecord 是单一候选身份的校准恢复状态。
type CalibrationCheckpointRecord struct {
	Identity           string
	SchemaVersion      uint32
	AcceptedGeneration uint64
	AgentTokenDigest   string
	Scenarios          []CalibrationCheckpointScenarioRecord
}

// LoadCalibrationCheckpoint 从 duration ledger SQLite authority 读取校准断点。
func (store *DurationLedgerStore) LoadCalibrationCheckpoint(identity, agentTokenDigest string) (CalibrationCheckpointRecord, bool, error) {
	if err := validateCalibrationCheckpointIdentity(identity, agentTokenDigest); err != nil {
		return CalibrationCheckpointRecord{}, false, err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return CalibrationCheckpointRecord{}, false, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("begin calibration checkpoint load", err)
	}
	defer transaction.Rollback()
	record, found, err := loadCalibrationCheckpointHeader(transaction, identity, agentTokenDigest)
	if err != nil || !found {
		return record, found, err
	}
	if err := loadCalibrationCheckpointScenarios(transaction, &record); err != nil {
		return CalibrationCheckpointRecord{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("commit calibration checkpoint load", err)
	}
	return record, true, nil
}

// validateCalibrationCheckpointIdentity 校验 checkpoint 身份与 agent digest 均可作为 SQLite 权威键。
func validateCalibrationCheckpointIdentity(identity, agentTokenDigest string) error {
	if strings.TrimSpace(identity) == "" || cicontract.ValidateAgentTokenDigest(agentTokenDigest) != nil {
		return fmt.Errorf("calibration checkpoint identity and agent token digest are required")
	}
	return nil
}

// loadCalibrationCheckpointHeader 读取并验证 checkpoint 元数据及其所属 agent。
func loadCalibrationCheckpointHeader(transaction *sql.Tx, identity, agentTokenDigest string) (CalibrationCheckpointRecord, bool, error) {
	record := CalibrationCheckpointRecord{Identity: identity}
	var acceptedGeneration string
	err := transaction.QueryRow(`SELECT schema_version, accepted_generation, agent_token_digest FROM remote_ci_calibration_checkpoints WHERE identity = ?`, identity).Scan(&record.SchemaVersion, &acceptedGeneration, &record.AgentTokenDigest)
	if errors.Is(err, sql.ErrNoRows) {
		return CalibrationCheckpointRecord{}, false, nil
	}
	if err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("load calibration checkpoint", err)
	}
	if err := cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest); err != nil {
		return CalibrationCheckpointRecord{}, false, fmt.Errorf("stored calibration checkpoint agent token digest: %w", err)
	}
	if record.AgentTokenDigest != agentTokenDigest {
		return CalibrationCheckpointRecord{}, false, fmt.Errorf("calibration checkpoint %q belongs to another agent", identity)
	}
	accepted, err := strconv.ParseUint(acceptedGeneration, 10, 64)
	if err != nil || accepted == 0 {
		return CalibrationCheckpointRecord{}, false, errors.New("stored calibration checkpoint accepted generation is invalid")
	}
	record.AcceptedGeneration = accepted
	return record, true, nil
}

// loadCalibrationCheckpointScenarios 读取并校验 checkpoint 已持久化的全部场景。
func loadCalibrationCheckpointScenarios(transaction *sql.Tx, record *CalibrationCheckpointRecord) error {
	rows, err := transaction.Query(`SELECT scenario, started, completed, input_json, result_json FROM remote_ci_calibration_checkpoint_scenarios WHERE identity = ? ORDER BY scenario`, record.Identity)
	if err != nil {
		return mapDurationLedgerSQLiteError("load calibration checkpoint scenarios", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scenario, inputJSON, resultJSON string
		var started, completed int
		if err := rows.Scan(&scenario, &started, &completed, &inputJSON, &resultJSON); err != nil {
			return mapDurationLedgerSQLiteError("scan calibration checkpoint scenario", err)
		}
		if started != 0 && started != 1 || completed != 0 && completed != 1 {
			return fmt.Errorf("calibration checkpoint %q has invalid boolean state", scenario)
		}
		record.Scenarios = append(record.Scenarios, CalibrationCheckpointScenarioRecord{Scenario: scenario, Started: started == 1, Completed: completed == 1, InputJSON: inputJSON, ResultJSON: resultJSON})
	}
	if err := rows.Err(); err != nil {
		return mapDurationLedgerSQLiteError("iterate calibration checkpoint scenarios", err)
	}
	return nil
}

// CreateCalibrationCheckpointIfAbsent 在单个事务中创建完整 checkpoint。
func (store *DurationLedgerStore) CreateCalibrationCheckpointIfAbsent(record CalibrationCheckpointRecord) (bool, error) {
	if err := validateCalibrationCheckpointRecord(record); err != nil {
		return false, err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return false, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return false, mapDurationLedgerSQLiteError("begin calibration checkpoint create", err)
	}
	defer transaction.Rollback()
	if err := requireHistoricallyAcceptedGeneration(transaction, record.AcceptedGeneration); err != nil {
		return false, err
	}
	created, err := store.createCalibrationCheckpointHeader(transaction, record)
	if err != nil {
		return false, err
	}
	if created == 0 {
		return false, commitExistingCalibrationCheckpoint(transaction)
	}
	if err := insertCalibrationCheckpointScenarios(transaction, record); err != nil {
		return false, err
	}
	if err := compactDurationLedgerAuthority(transaction); err != nil {
		return false, err
	}
	if err := transaction.Commit(); err != nil {
		return false, mapDurationLedgerSQLiteError("commit calibration checkpoint create", err)
	}
	return true, nil
}

// commitExistingCalibrationCheckpoint 提交未新建数据的读取事务以保持原子判定。
func commitExistingCalibrationCheckpoint(transaction *sql.Tx) error {
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit existing calibration checkpoint", err)
	}
	return nil
}

// validateCalibrationCheckpointRecord 校验创建 checkpoint 所需的全部权威身份字段。
func validateCalibrationCheckpointRecord(record CalibrationCheckpointRecord) error {
	if strings.TrimSpace(record.Identity) == "" || record.SchemaVersion == 0 || record.AcceptedGeneration == 0 || cicontract.ValidateAgentTokenDigest(record.AgentTokenDigest) != nil {
		return fmt.Errorf("calibration checkpoint identity, schema version, and agent token digest are required")
	}
	return nil
}

// createCalibrationCheckpointHeader 插入 checkpoint 头并返回该身份是否由本事务新建。
func (store *DurationLedgerStore) createCalibrationCheckpointHeader(transaction *sql.Tx, record CalibrationCheckpointRecord) (int64, error) {
	result, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, accepted_generation, agent_token_digest, updated_at_unix_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT(identity) DO NOTHING`, record.Identity, record.SchemaVersion, strconv.FormatUint(record.AcceptedGeneration, 10), record.AgentTokenDigest, store.nowFunc().UTC().UnixMilli())
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("create calibration checkpoint", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return 0, mapDurationLedgerSQLiteError("inspect calibration checkpoint create", err)
	}
	return created, nil
}

// insertCalibrationCheckpointScenarios 在创建 checkpoint 的同一事务中写入并校验所有场景。
func insertCalibrationCheckpointScenarios(transaction *sql.Tx, record CalibrationCheckpointRecord) error {
	for _, scenario := range record.Scenarios {
		if !validCalibrationCheckpointScenario(scenario) {
			return fmt.Errorf("calibration checkpoint scenario %q is invalid", scenario.Scenario)
		}
		if _, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoint_scenarios (identity, scenario, started, completed, input_json, result_json) VALUES (?, ?, ?, ?, ?, ?)`, record.Identity, scenario.Scenario, boolToSQLiteCheckpoint(scenario.Started), boolToSQLiteCheckpoint(scenario.Completed), scenario.InputJSON, scenario.ResultJSON); err != nil {
			return mapDurationLedgerSQLiteError("insert calibration checkpoint scenario", err)
		}
	}
	return nil
}

// CompareAndSwapCalibrationCheckpointScenario 原子写入一个场景，并拒绝过期的全量读取结果。
func (store *DurationLedgerStore) CompareAndSwapCalibrationCheckpointScenario(identity, agentTokenDigest string, schemaVersion uint32, acceptedGeneration uint64, expected *CalibrationCheckpointScenarioRecord, next CalibrationCheckpointScenarioRecord) error {
	if err := validateCalibrationCheckpointScenarioUpdate(identity, agentTokenDigest, schemaVersion, acceptedGeneration, next); err != nil {
		return err
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		return mapDurationLedgerSQLiteError("begin calibration checkpoint scenario CAS", err)
	}
	defer transaction.Rollback()
	if err := requireHistoricallyAcceptedGeneration(transaction, acceptedGeneration); err != nil {
		return err
	}
	if err := store.upsertCalibrationCheckpointHeader(transaction, identity, agentTokenDigest, schemaVersion, acceptedGeneration); err != nil {
		return err
	}
	if err := writeCalibrationCheckpointScenario(transaction, identity, expected, next); err != nil {
		return err
	}
	if err := compactDurationLedgerAuthority(transaction); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit calibration checkpoint scenario CAS", err)
	}
	return nil
}

// validateCalibrationCheckpointScenarioUpdate 校验场景 CAS 的身份、版本、代际和新载荷。
func validateCalibrationCheckpointScenarioUpdate(identity, agentTokenDigest string, schemaVersion uint32, acceptedGeneration uint64, next CalibrationCheckpointScenarioRecord) error {
	if strings.TrimSpace(identity) == "" || schemaVersion == 0 || acceptedGeneration == 0 || cicontract.ValidateAgentTokenDigest(agentTokenDigest) != nil || !validCalibrationCheckpointScenario(next) {
		return fmt.Errorf("calibration checkpoint scenario update is invalid")
	}
	return nil
}

// upsertCalibrationCheckpointHeader 以身份、版本、代际和 agent digest 作为 CAS 前置条件更新头记录。
func (store *DurationLedgerStore) upsertCalibrationCheckpointHeader(transaction *sql.Tx, identity, agentTokenDigest string, schemaVersion uint32, acceptedGeneration uint64) error {
	result, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, accepted_generation, agent_token_digest, updated_at_unix_ms) VALUES (?, ?, ?, ?, ?) ON CONFLICT(identity) DO UPDATE SET updated_at_unix_ms = excluded.updated_at_unix_ms WHERE remote_ci_calibration_checkpoints.schema_version = excluded.schema_version AND remote_ci_calibration_checkpoints.accepted_generation = excluded.accepted_generation AND remote_ci_calibration_checkpoints.agent_token_digest = excluded.agent_token_digest`, identity, schemaVersion, strconv.FormatUint(acceptedGeneration, 10), agentTokenDigest, store.nowFunc().UTC().UnixMilli())
	if err != nil {
		return mapDurationLedgerSQLiteError("upsert calibration checkpoint header", err)
	}
	return requireCalibrationCheckpointChange(result, "inspect calibration checkpoint header")
}

// writeCalibrationCheckpointScenario 根据读取时的场景快照执行新增或条件更新。
func writeCalibrationCheckpointScenario(transaction *sql.Tx, identity string, expected *CalibrationCheckpointScenarioRecord, next CalibrationCheckpointScenarioRecord) error {
	if expected == nil {
		return insertCalibrationCheckpointScenario(transaction, identity, next)
	}
	return updateCalibrationCheckpointScenario(transaction, identity, *expected, next)
}

// insertCalibrationCheckpointScenario 仅在场景尚不存在时插入，并把并发写入报告为冲突。
func insertCalibrationCheckpointScenario(transaction *sql.Tx, identity string, next CalibrationCheckpointScenarioRecord) error {
	result, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoint_scenarios (identity, scenario, started, completed, input_json, result_json) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(identity, scenario) DO NOTHING`, identity, next.Scenario, boolToSQLiteCheckpoint(next.Started), boolToSQLiteCheckpoint(next.Completed), next.InputJSON, next.ResultJSON)
	if err != nil {
		return mapDurationLedgerSQLiteError("insert calibration checkpoint scenario", err)
	}
	return requireCalibrationCheckpointChange(result, "inspect calibration checkpoint scenario insert")
}

// updateCalibrationCheckpointScenario 以完整旧场景载荷为 CAS 条件更新场景。
func updateCalibrationCheckpointScenario(transaction *sql.Tx, identity string, expected, next CalibrationCheckpointScenarioRecord) error {
	if !validCalibrationCheckpointScenario(expected) || expected.Scenario != next.Scenario {
		return fmt.Errorf("calibration checkpoint expected scenario is invalid")
	}
	result, err := transaction.Exec(`UPDATE remote_ci_calibration_checkpoint_scenarios SET started = ?, completed = ?, input_json = ?, result_json = ? WHERE identity = ? AND scenario = ? AND started = ? AND completed = ? AND input_json = ? AND result_json = ?`, boolToSQLiteCheckpoint(next.Started), boolToSQLiteCheckpoint(next.Completed), next.InputJSON, next.ResultJSON, identity, next.Scenario, boolToSQLiteCheckpoint(expected.Started), boolToSQLiteCheckpoint(expected.Completed), expected.InputJSON, expected.ResultJSON)
	if err != nil {
		return mapDurationLedgerSQLiteError("update calibration checkpoint scenario", err)
	}
	return requireCalibrationCheckpointChange(result, "inspect calibration checkpoint scenario update")
}

// requireCalibrationCheckpointChange 将 SQLite 行数检查统一映射为权威 CAS 冲突。
func requireCalibrationCheckpointChange(result sql.Result, operation string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return mapDurationLedgerSQLiteError(operation, err)
	}
	if changed == 0 {
		return ErrCalibrationCheckpointConflict
	}
	return nil
}

// DeleteCalibrationCheckpoint 删除已经接受的校准断点。
func (store *DurationLedgerStore) DeleteCalibrationCheckpoint(identity, agentTokenDigest string) error {
	if strings.TrimSpace(identity) == "" || cicontract.ValidateAgentTokenDigest(agentTokenDigest) != nil {
		return fmt.Errorf("calibration checkpoint identity and agent token digest are required")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	return withSQLiteWriteTransaction(database, "delete calibration checkpoint", func(transaction *sql.Tx) error {
		if _, err := transaction.Exec(`DELETE FROM remote_ci_calibration_checkpoints WHERE identity = ? AND agent_token_digest = ?`, identity, agentTokenDigest); err != nil {
			return mapDurationLedgerSQLiteError("delete calibration checkpoint", err)
		}
		return compactDurationLedgerAuthority(transaction)
	})
}

func boolToSQLiteCheckpoint(value bool) int {
	if value {
		return 1
	}
	return 0
}

// validCalibrationCheckpointScenario 校验场景名称、启动状态及载荷与完成状态的一致性。
func validCalibrationCheckpointScenario(scenario CalibrationCheckpointScenarioRecord) bool {
	return strings.TrimSpace(scenario.Scenario) != "" && scenario.Started &&
		(!scenario.Completed && scenario.InputJSON == "" && scenario.ResultJSON == "" || scenario.Completed && scenario.InputJSON != "" && scenario.ResultJSON != "")
}
