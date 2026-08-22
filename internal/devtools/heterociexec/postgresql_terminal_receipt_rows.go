package heterociexec

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"
)

func readPostgreSQLReceiptEvent(ctx context.Context, queryer *sql.Tx, operationID string, seq int64) (postgresqlReceiptEventRow, bool, error) {
	var row postgresqlReceiptEventRow
	err := scanPostgreSQLReceiptEvent(queryer.QueryRowContext(ctx, selectPostgreSQLReceiptEvent, operationID, seq), &row)
	if err == sql.ErrNoRows {
		return postgresqlReceiptEventRow{}, false, nil
	}
	if err != nil {
		return postgresqlReceiptEventRow{}, false, fmt.Errorf("%w: read receipt event: %w", ErrPostgreSQLReceiptStore, err)
	}
	return row, true, nil
}

func readPostgreSQLReceiptEventHead(ctx context.Context, queryer *sql.Tx, operationID string) (postgresqlReceiptEventRow, bool, error) {
	var row postgresqlReceiptEventRow
	err := scanPostgreSQLReceiptEvent(queryer.QueryRowContext(ctx, selectPostgreSQLReceiptEventHead, operationID), &row)
	if err == sql.ErrNoRows {
		return postgresqlReceiptEventRow{}, false, nil
	}
	if err != nil {
		return postgresqlReceiptEventRow{}, false, fmt.Errorf("%w: read receipt event head: %w", ErrPostgreSQLReceiptStore, err)
	}
	return row, true, nil
}

func readPostgreSQLReceiptEventChain(ctx context.Context, queryer *sql.Tx, operationID string) ([]PostgreSQLReceiptEvent, error) {
	rows, err := queryer.QueryContext(ctx, selectPostgreSQLReceiptEvents, operationID)
	if err != nil {
		return nil, fmt.Errorf("%w: read receipt event chain: %w", ErrPostgreSQLReceiptStore, err)
	}
	defer rows.Close()
	return scanPostgreSQLReceiptEvents(rows)
}

func scanPostgreSQLReceiptEvent(scanner interface{ Scan(...any) error }, row *postgresqlReceiptEventRow) error {
	return scanner.Scan(&row.schemaVersion, &row.operationID, &row.manifestDigest, &row.planDigest, &row.nodeID, &row.executorKind, &row.authorityGeneration, &row.seq, &row.occurredAt, &row.stage, &row.state, &row.nodeIDDigest, &row.code, &row.eventBytes, &row.eventDigest)
}

func scanPostgreSQLReceiptEvents(rows *sql.Rows) ([]PostgreSQLReceiptEvent, error) {
	events := make([]PostgreSQLReceiptEvent, 0)
	for rows.Next() {
		var row postgresqlReceiptEventRow
		if err := scanPostgreSQLReceiptEvent(rows, &row); err != nil {
			return nil, fmt.Errorf("%w: scan receipt event: %w", ErrPostgreSQLReceiptStore, err)
		}
		event, err := decodePostgreSQLReceiptEvent(row.eventBytes, row.eventDigest)
		if err != nil {
			return nil, err
		}
		if err := validatePostgreSQLReceiptEventRow(row, event); err != nil {
			return nil, err
		}
		if len(events) > 0 && event.Seq != events[len(events)-1].Seq+1 {
			return nil, fmt.Errorf("%w: event sequence is not contiguous", ErrPostgreSQLReceiptStore)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: iterate receipt events: %w", ErrPostgreSQLReceiptStore, err)
	}
	return events, nil
}

func readPostgreSQLTerminalReceipt(ctx context.Context, queryer *sql.Tx, operationID string) (PostgreSQLTerminalReceipt, bool, error) {
	var row postgresqlTerminalReceiptRow
	err := queryer.QueryRowContext(ctx, selectPostgreSQLTerminalReceipt, operationID).Scan(&row.schemaVersion, &row.operationID, &row.requestDigest, &row.manifestDigest, &row.planDigest, &row.nodeID, &row.executorKind, &row.authorityGeneration, &row.startedAt, &row.finishedAt, &row.exitCode, &row.cleanupState, &row.errorCode, &row.capacityCode, &row.status, &row.eventSeq, &row.eventDigest, &row.receiptBytes, &row.receiptDigest)
	if err == sql.ErrNoRows {
		return PostgreSQLTerminalReceipt{}, false, nil
	}
	if err != nil {
		return PostgreSQLTerminalReceipt{}, false, fmt.Errorf("%w: read terminal receipt: %w", ErrPostgreSQLReceiptStore, err)
	}
	receipt, err := decodePostgreSQLTerminalReceipt(row.receiptBytes, row.receiptDigest)
	if err != nil {
		return PostgreSQLTerminalReceipt{}, false, err
	}
	if err := validatePostgreSQLTerminalReceiptRow(row, receipt); err != nil {
		return PostgreSQLTerminalReceipt{}, false, err
	}
	return receipt, true, nil
}

// PostgreSQLReceiptLifecycleStages 返回 terminal receipt 必须覆盖的八阶段顺序。
func PostgreSQLReceiptLifecycleStages() []PostgreSQLReceiptEventStage {
	return []PostgreSQLReceiptEventStage{PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStageManifestBinding, PostgreSQLReceiptEventStageCapacitySnapshot, PostgreSQLReceiptEventStageReservation, PostgreSQLReceiptEventStageMaterialize, PostgreSQLReceiptEventStageExecute, PostgreSQLReceiptEventStageCleanup, PostgreSQLReceiptEventStageTerminal}
}

func validatePostgreSQLReceiptEventChain(events []PostgreSQLReceiptEvent, receipt PostgreSQLTerminalReceipt) error {
	stages := PostgreSQLReceiptLifecycleStages()
	if len(events) != len(stages) || receipt.EventSeq != int64(len(stages)) {
		return fmt.Errorf("%w: terminal receipt requires complete lifecycle event chain", ErrPostgreSQLReceiptConflict)
	}
	for index, event := range events {
		if err := validatePostgreSQLReceiptEventPosition(event, index, stages[index]); err != nil {
			return err
		}
		if err := validatePostgreSQLReceiptChainIdentity(event, receipt); err != nil {
			return err
		}
	}
	last := events[len(events)-1]
	if last.EventDigest != receipt.EventDigest || !validPostgreSQLReceiptTerminalState(string(last.State), receipt.Status) {
		return fmt.Errorf("%w: lifecycle terminal event is not exact", ErrPostgreSQLReceiptConflict)
	}
	if err := validatePostgreSQLTerminalEventCode(last, receipt); err != nil {
		return err
	}
	if receipt.Status == PostgreSQLReceiptStatusBlocked {
		return validatePostgreSQLBlockedLifecycle(events)
	}
	return validatePostgreSQLCompletedLifecycle(events, receipt.Status)
}

func validatePostgreSQLTerminalEventCode(event PostgreSQLReceiptEvent, receipt PostgreSQLTerminalReceipt) error {
	switch receipt.Status {
	case PostgreSQLReceiptStatusSucceeded:
		if event.Code != "" {
			return fmt.Errorf("%w: succeeded terminal event carries code", ErrPostgreSQLReceiptConflict)
		}
	case PostgreSQLReceiptStatusFailed:
		if receipt.ErrorCode == "" || event.Code != receipt.ErrorCode {
			return fmt.Errorf("%w: failed terminal event code drifted", ErrPostgreSQLReceiptConflict)
		}
	case PostgreSQLReceiptStatusBlocked:
		if receipt.CapacityCode == "" || event.Code != receipt.CapacityCode {
			return fmt.Errorf("%w: blocked terminal event code drifted", ErrPostgreSQLReceiptConflict)
		}
	default:
		return fmt.Errorf("%w: terminal status is invalid", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validPostgreSQLCompletedStageState(state PostgreSQLReceiptEventState) bool {
	return state == PostgreSQLReceiptEventStateCompleted || state == PostgreSQLReceiptEventStateSucceeded
}

func validatePostgreSQLBlockedLifecycle(events []PostgreSQLReceiptEvent) error {
	if !validPostgreSQLCompletedStageState(events[0].State) || !validPostgreSQLCompletedStageState(events[1].State) {
		return fmt.Errorf("%w: blocked lifecycle lacks request or manifest evidence", ErrPostgreSQLReceiptConflict)
	}
	if events[2].State != PostgreSQLReceiptEventStateBlocked || events[2].Code == "" {
		return fmt.Errorf("%w: blocked lifecycle lacks capacity blocker evidence", ErrPostgreSQLReceiptConflict)
	}
	if events[7].State != PostgreSQLReceiptEventStateBlocked || events[7].Code == "" {
		return fmt.Errorf("%w: blocked lifecycle lacks terminal blocker evidence", ErrPostgreSQLReceiptConflict)
	}
	for index := 3; index < 7; index++ {
		if events[index].State != PostgreSQLReceiptEventStateSkipped || events[index].Code == "" {
			return fmt.Errorf("%w: blocked lifecycle lacks skipped stage evidence", ErrPostgreSQLReceiptConflict)
		}
	}
	return nil
}

func validatePostgreSQLCompletedLifecycle(events []PostgreSQLReceiptEvent, status string) error {
	if status == PostgreSQLReceiptStatusFailed {
		return validatePostgreSQLFailedLifecycle(events)
	}
	if status != PostgreSQLReceiptStatusSucceeded {
		return fmt.Errorf("%w: lifecycle status is invalid", ErrPostgreSQLReceiptConflict)
	}
	for index, event := range events[:7] {
		if !validPostgreSQLCompletedStageState(event.State) {
			return fmt.Errorf("%w: %s lifecycle has incomplete stage %d", ErrPostgreSQLReceiptConflict, status, index+1)
		}
	}
	return nil
}

func validatePostgreSQLFailedLifecycle(events []PostgreSQLReceiptEvent) error {
	failureIndex, failures := postgreSQLFailureStage(events)
	if failures != 1 || (failureIndex != 4 && failureIndex != 5) {
		return fmt.Errorf("%w: failed lifecycle must have one materialize or execute failure", ErrPostgreSQLReceiptConflict)
	}
	if events[failureIndex].Code == "" || !validPostgreSQLCompletedPrefix(events, failureIndex) {
		return fmt.Errorf("%w: failed lifecycle prefix or failure code is incomplete", ErrPostgreSQLReceiptConflict)
	}
	for index := failureIndex + 1; index < 6; index++ {
		if events[index].State != PostgreSQLReceiptEventStateSkipped || events[index].Code == "" {
			return fmt.Errorf("%w: failed lifecycle skipped evidence is incomplete", ErrPostgreSQLReceiptConflict)
		}
	}
	if !validPostgreSQLCompletedStageState(events[6].State) {
		return fmt.Errorf("%w: failed lifecycle cleanup is not proven", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func postgreSQLFailureStage(events []PostgreSQLReceiptEvent) (int, int) {
	failureIndex, failures := -1, 0
	for index := range 7 {
		if events[index].State == PostgreSQLReceiptEventStateFailed {
			failureIndex, failures = index, failures+1
		}
	}
	return failureIndex, failures
}

func validPostgreSQLCompletedPrefix(events []PostgreSQLReceiptEvent, failureIndex int) bool {
	for index := range failureIndex {
		if !validPostgreSQLCompletedStageState(events[index].State) {
			return false
		}
	}
	return true
}

func validatePostgreSQLReceiptEventPosition(event PostgreSQLReceiptEvent, index int, stage PostgreSQLReceiptEventStage) error {
	if event.Seq != int64(index+1) || event.Stage != stage {
		return fmt.Errorf("%w: lifecycle event position is not exact", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validatePostgreSQLReceiptChainIdentity(event PostgreSQLReceiptEvent, receipt PostgreSQLTerminalReceipt) error {
	if event.OperationID != receipt.OperationID || event.ManifestDigest != receipt.ManifestDigest || event.PlanDigest != receipt.PlanDigest || event.NodeID != receipt.NodeID || event.ExecutorKind != receipt.ExecutorKind || event.AuthorityGeneration != receipt.AuthorityGeneration {
		return fmt.Errorf("%w: lifecycle event identity is not exact", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func validatePostgreSQLReceiptEventReplay(row postgresqlReceiptEventRow, want PostgreSQLReceiptEvent, wantBytes []byte, wantDigest string) error {
	got, err := decodePostgreSQLReceiptEvent(row.eventBytes, row.eventDigest)
	if err != nil {
		return err
	}
	if !bytes.Equal(row.eventBytes, wantBytes) || row.eventDigest != wantDigest || got != want {
		return fmt.Errorf("%w: event replay differs", ErrPostgreSQLReceiptConflict)
	}
	return validatePostgreSQLReceiptEventRow(row, got)
}

func validatePostgreSQLReceiptEventCore(row postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) bool {
	return row.schemaVersion == event.SchemaVersion && row.operationID == event.OperationID && row.manifestDigest == event.ManifestDigest && row.planDigest == event.PlanDigest && row.nodeID == event.NodeID && row.executorKind == event.ExecutorKind
}

func validatePostgreSQLReceiptEventNumbers(row postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) bool {
	return row.authorityGeneration == event.AuthorityGeneration && row.seq == event.Seq
}

func validatePostgreSQLReceiptEventDetails(row postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) bool {
	return row.occurredAt == event.OccurredAt.Format(time.RFC3339Nano) && row.stage == string(event.Stage) && row.state == string(event.State) && row.nodeIDDigest == event.NodeIDDigest && row.code == event.Code
}

func validatePostgreSQLReceiptEventRow(row postgresqlReceiptEventRow, event PostgreSQLReceiptEvent) error {
	if !validatePostgreSQLReceiptEventCore(row, event) || !validatePostgreSQLReceiptEventNumbers(row, event) || !validatePostgreSQLReceiptEventDetails(row, event) {
		return fmt.Errorf("%w: event structured columns drifted", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validatePostgreSQLTerminalReceiptReplay(persisted, want PostgreSQLTerminalReceipt, wantBytes []byte, wantDigest string) error {
	persistedBytes, persistedDigest, err := canonicalPostgreSQLTerminalReceipt(persisted)
	if err != nil {
		return err
	}
	if persisted != want || !bytes.Equal(wantBytes, persistedBytes) || persistedDigest != wantDigest {
		return fmt.Errorf("%w: terminal receipt replay differs", ErrPostgreSQLReceiptConflict)
	}
	return nil
}

func postgreSQLReceiptStageOrder(stage PostgreSQLReceiptEventStage) int {
	for index, candidate := range []PostgreSQLReceiptEventStage{
		PostgreSQLReceiptEventStageRequestValidation, PostgreSQLReceiptEventStageManifestBinding, PostgreSQLReceiptEventStageCapacitySnapshot, PostgreSQLReceiptEventStageReservation, PostgreSQLReceiptEventStageMaterialize, PostgreSQLReceiptEventStageExecute, PostgreSQLReceiptEventStageCleanup, PostgreSQLReceiptEventStageTerminal,
	} {
		if stage == candidate {
			return index
		}
	}
	return -1
}

func validatePostgreSQLTerminalReceiptHeaderRow(row postgresqlTerminalReceiptRow, receipt PostgreSQLTerminalReceipt) bool {
	return row.schemaVersion == receipt.SchemaVersion && row.operationID == receipt.OperationID && row.requestDigest == receipt.RequestDigest && row.manifestDigest == receipt.ManifestDigest && row.planDigest == receipt.PlanDigest
}

func validatePostgreSQLTerminalReceiptBindingRow(row postgresqlTerminalReceiptRow, receipt PostgreSQLTerminalReceipt) bool {
	return row.nodeID == receipt.NodeID && row.executorKind == receipt.ExecutorKind && row.authorityGeneration == receipt.AuthorityGeneration
}

func validatePostgreSQLTerminalReceiptTimeRow(row postgresqlTerminalReceiptRow, receipt PostgreSQLTerminalReceipt) bool {
	return row.startedAt == receipt.StartedAt.Format(time.RFC3339Nano) && row.finishedAt == receipt.FinishedAt.Format(time.RFC3339Nano)
}

func validatePostgreSQLTerminalReceiptOutcomeRow(row postgresqlTerminalReceiptRow, receipt PostgreSQLTerminalReceipt) bool {
	return row.exitCode == receipt.ExitCode && row.cleanupState == receipt.CleanupState && row.errorCode == receipt.ErrorCode && row.capacityCode == receipt.CapacityCode && row.status == receipt.Status && row.eventSeq == receipt.EventSeq && row.eventDigest == receipt.EventDigest
}

func validatePostgreSQLTerminalReceiptRow(row postgresqlTerminalReceiptRow, receipt PostgreSQLTerminalReceipt) error {
	if !validatePostgreSQLTerminalReceiptHeaderRow(row, receipt) || !validatePostgreSQLTerminalReceiptBindingRow(row, receipt) || !validatePostgreSQLTerminalReceiptTimeRow(row, receipt) || !validatePostgreSQLTerminalReceiptOutcomeRow(row, receipt) {
		return fmt.Errorf("%w: terminal receipt structured columns drifted", ErrPostgreSQLReceiptStore)
	}
	return nil
}

func validPostgreSQLReceiptTerminalState(eventState, status string) bool {
	switch status {
	case PostgreSQLReceiptStatusSucceeded:
		return eventState == string(PostgreSQLReceiptEventStateSucceeded) || eventState == string(PostgreSQLReceiptEventStateCompleted)
	case PostgreSQLReceiptStatusFailed:
		return eventState == string(PostgreSQLReceiptEventStateFailed)
	case PostgreSQLReceiptStatusBlocked:
		return eventState == string(PostgreSQLReceiptEventStateBlocked)
	default:
		return false
	}
}
