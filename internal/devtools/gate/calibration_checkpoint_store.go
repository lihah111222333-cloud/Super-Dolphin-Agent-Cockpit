package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	Identity      string
	SchemaVersion uint32
	Scenarios     []CalibrationCheckpointScenarioRecord
}

// LoadCalibrationCheckpoint 从 duration ledger SQLite authority 读取校准断点。
func (store *DurationLedgerStore) LoadCalibrationCheckpoint(identity string) (CalibrationCheckpointRecord, bool, error) {
	if strings.TrimSpace(identity) == "" {
		return CalibrationCheckpointRecord{}, false, fmt.Errorf("calibration checkpoint identity is required")
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
	record := CalibrationCheckpointRecord{Identity: identity}
	var schemaVersion uint32
	err = transaction.QueryRow(`SELECT schema_version FROM remote_ci_calibration_checkpoints WHERE identity = ?`, identity).Scan(&schemaVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return CalibrationCheckpointRecord{}, false, nil
	}
	if err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("load calibration checkpoint", err)
	}
	record.SchemaVersion = schemaVersion
	rows, err := transaction.Query(`SELECT scenario, started, completed, input_json, result_json FROM remote_ci_calibration_checkpoint_scenarios WHERE identity = ? ORDER BY scenario`, identity)
	if err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("load calibration checkpoint scenarios", err)
	}
	defer rows.Close()
	for rows.Next() {
		var scenario, inputJSON, resultJSON string
		var started, completed int
		if err := rows.Scan(&scenario, &started, &completed, &inputJSON, &resultJSON); err != nil {
			return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("scan calibration checkpoint scenario", err)
		}
		if started != 0 && started != 1 || completed != 0 && completed != 1 {
			return CalibrationCheckpointRecord{}, false, fmt.Errorf("calibration checkpoint %q has invalid boolean state", scenario)
		}
		record.Scenarios = append(record.Scenarios, CalibrationCheckpointScenarioRecord{Scenario: scenario, Started: started == 1, Completed: completed == 1, InputJSON: inputJSON, ResultJSON: resultJSON})
	}
	if err := rows.Err(); err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("iterate calibration checkpoint scenarios", err)
	}
	if err := transaction.Commit(); err != nil {
		return CalibrationCheckpointRecord{}, false, mapDurationLedgerSQLiteError("commit calibration checkpoint load", err)
	}
	return record, true, nil
}

// CreateCalibrationCheckpointIfAbsent 在单个事务中创建从 legacy JSON 导入的完整 checkpoint。
func (store *DurationLedgerStore) CreateCalibrationCheckpointIfAbsent(record CalibrationCheckpointRecord) (bool, error) {
	if strings.TrimSpace(record.Identity) == "" || record.SchemaVersion == 0 {
		return false, fmt.Errorf("calibration checkpoint identity and schema version are required")
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
	result, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, updated_at_unix_ms) VALUES (?, ?, ?) ON CONFLICT(identity) DO NOTHING`, record.Identity, record.SchemaVersion, store.nowFunc().UTC().UnixMilli())
	if err != nil {
		return false, mapDurationLedgerSQLiteError("create calibration checkpoint", err)
	}
	created, err := result.RowsAffected()
	if err != nil {
		return false, mapDurationLedgerSQLiteError("inspect calibration checkpoint create", err)
	}
	if created == 0 {
		if err := transaction.Commit(); err != nil {
			return false, mapDurationLedgerSQLiteError("commit existing calibration checkpoint", err)
		}
		return false, nil
	}
	for _, scenario := range record.Scenarios {
		if strings.TrimSpace(scenario.Scenario) == "" || !scenario.Started || (!scenario.Completed && (scenario.InputJSON != "" || scenario.ResultJSON != "")) || (scenario.Completed && (scenario.InputJSON == "" || scenario.ResultJSON == "")) {
			return false, fmt.Errorf("calibration checkpoint scenario %q is invalid", scenario.Scenario)
		}
		if _, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoint_scenarios (identity, scenario, started, completed, input_json, result_json) VALUES (?, ?, ?, ?, ?, ?)`, record.Identity, scenario.Scenario, boolToSQLiteCheckpoint(scenario.Started), boolToSQLiteCheckpoint(scenario.Completed), scenario.InputJSON, scenario.ResultJSON); err != nil {
			return false, mapDurationLedgerSQLiteError("insert calibration checkpoint scenario", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return false, mapDurationLedgerSQLiteError("commit calibration checkpoint create", err)
	}
	return true, nil
}

// CompareAndSwapCalibrationCheckpointScenario 原子写入一个场景，并拒绝过期的全量读取结果。
func (store *DurationLedgerStore) CompareAndSwapCalibrationCheckpointScenario(identity string, schemaVersion uint32, expected *CalibrationCheckpointScenarioRecord, next CalibrationCheckpointScenarioRecord) error {
	if strings.TrimSpace(identity) == "" || schemaVersion == 0 || !validCalibrationCheckpointScenario(next) {
		return fmt.Errorf("calibration checkpoint scenario update is invalid")
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
	if _, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoints (identity, schema_version, updated_at_unix_ms) VALUES (?, ?, ?) ON CONFLICT(identity) DO UPDATE SET updated_at_unix_ms = excluded.updated_at_unix_ms`, identity, schemaVersion, store.nowFunc().UTC().UnixMilli()); err != nil {
		return mapDurationLedgerSQLiteError("upsert calibration checkpoint header", err)
	}
	if expected == nil {
		result, err := transaction.Exec(`INSERT INTO remote_ci_calibration_checkpoint_scenarios (identity, scenario, started, completed, input_json, result_json) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(identity, scenario) DO NOTHING`, identity, next.Scenario, boolToSQLiteCheckpoint(next.Started), boolToSQLiteCheckpoint(next.Completed), next.InputJSON, next.ResultJSON)
		if err != nil {
			return mapDurationLedgerSQLiteError("insert calibration checkpoint scenario", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return mapDurationLedgerSQLiteError("inspect calibration checkpoint scenario insert", err)
		}
		if changed == 0 {
			return ErrCalibrationCheckpointConflict
		}
	} else {
		if !validCalibrationCheckpointScenario(*expected) || expected.Scenario != next.Scenario {
			return fmt.Errorf("calibration checkpoint expected scenario is invalid")
		}
		result, err := transaction.Exec(`UPDATE remote_ci_calibration_checkpoint_scenarios SET started = ?, completed = ?, input_json = ?, result_json = ? WHERE identity = ? AND scenario = ? AND started = ? AND completed = ? AND input_json = ? AND result_json = ?`, boolToSQLiteCheckpoint(next.Started), boolToSQLiteCheckpoint(next.Completed), next.InputJSON, next.ResultJSON, identity, next.Scenario, boolToSQLiteCheckpoint(expected.Started), boolToSQLiteCheckpoint(expected.Completed), expected.InputJSON, expected.ResultJSON)
		if err != nil {
			return mapDurationLedgerSQLiteError("update calibration checkpoint scenario", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return mapDurationLedgerSQLiteError("inspect calibration checkpoint scenario update", err)
		}
		if changed == 0 {
			return ErrCalibrationCheckpointConflict
		}
	}
	if err := transaction.Commit(); err != nil {
		return mapDurationLedgerSQLiteError("commit calibration checkpoint scenario CAS", err)
	}
	return nil
}

// DeleteCalibrationCheckpoint 删除已经接受的校准断点。
func (store *DurationLedgerStore) DeleteCalibrationCheckpoint(identity string) error {
	if strings.TrimSpace(identity) == "" {
		return fmt.Errorf("calibration checkpoint identity is required")
	}
	database, err := store.openSQLiteAuthority(true)
	if err != nil {
		return err
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM remote_ci_calibration_checkpoints WHERE identity = ?`, identity); err != nil {
		return mapDurationLedgerSQLiteError("delete calibration checkpoint", err)
	}
	return nil
}

func boolToSQLiteCheckpoint(value bool) int {
	if value {
		return 1
	}
	return 0
}

func validCalibrationCheckpointScenario(scenario CalibrationCheckpointScenarioRecord) bool {
	return strings.TrimSpace(scenario.Scenario) != "" && scenario.Started &&
		(!scenario.Completed && scenario.InputJSON == "" && scenario.ResultJSON == "" || scenario.Completed && scenario.InputJSON != "" && scenario.ResultJSON != "")
}
