package heterociexec

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
)

type postgresqlStructuredTestAuthority struct {
	currentUser       string
	readOnly          string
	synchronousCommit string
	inRecovery        bool
	serverAddress     string
	tableExists       bool
	eventTableExists  bool
	schemaOwner       string
	tableOwnership    string
	indexNames        string
}

func validPostgreSQLStructuredTestAuthority() postgresqlStructuredTestAuthority {
	return postgresqlStructuredTestAuthority{
		currentUser: PostgreSQLReceiptWriterRole, readOnly: "off", synchronousCommit: "remote_apply",
		serverAddress: "10.20.30.40", tableExists: true, eventTableExists: true,
		schemaOwner: PostgreSQLReceiptWriterRole, tableOwnership: expectedPostgreSQLReceiptTableOwnership(),
		indexNames: expectedPostgreSQLReceiptIndexNames(),
	}
}

type postgresqlStructuredTestRow struct {
	receipt *postgresqlTerminalReceiptRow
	events  map[int64]*postgresqlReceiptEventRow
}

type postgresqlStructuredTestState struct {
	mutex           sync.Mutex
	authority       postgresqlStructuredTestAuthority
	row             postgresqlStructuredTestRow
	nextCommitError error
}

func newPostgreSQLStructuredTestState() *postgresqlStructuredTestState {
	return &postgresqlStructuredTestState{authority: validPostgreSQLStructuredTestAuthority(), row: postgresqlStructuredTestRow{events: make(map[int64]*postgresqlReceiptEventRow)}}
}

func (state *postgresqlStructuredTestState) setCommitError(err error) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.nextCommitError = err
}

func clonePostgreSQLStructuredTestRow(row postgresqlStructuredTestRow) postgresqlStructuredTestRow {
	clone := postgresqlStructuredTestRow{events: make(map[int64]*postgresqlReceiptEventRow, len(row.events))}
	if row.receipt != nil {
		value := *row.receipt
		value.receiptBytes = bytes.Clone(row.receipt.receiptBytes)
		clone.receipt = &value
	}
	for seq, event := range row.events {
		value := *event
		value.eventBytes = bytes.Clone(event.eventBytes)
		clone.events[seq] = &value
	}
	return clone
}

type postgresqlStructuredTestConnector struct {
	state *postgresqlStructuredTestState
}

func (connector postgresqlStructuredTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &postgresqlStructuredTestConnection{state: connector.state}, nil
}

func (connector postgresqlStructuredTestConnector) Driver() driver.Driver {
	return postgresqlStructuredTestDriver{}
}

type postgresqlStructuredTestDriver struct{}

func (postgresqlStructuredTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("structured test driver requires connector")
}

type postgresqlStructuredTestConnection struct {
	state  *postgresqlStructuredTestState
	active *postgresqlStructuredTestTransaction
}

func (connection *postgresqlStructuredTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are forbidden")
}

func (*postgresqlStructuredTestConnection) Close() error { return nil }

func (*postgresqlStructuredTestConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction options are required")
}

func (connection *postgresqlStructuredTestConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelSerializable) || options.ReadOnly || connection.active != nil {
		return nil, fmt.Errorf("invalid transaction options: %#v", options)
	}
	connection.state.mutex.Lock()
	row := clonePostgreSQLStructuredTestRow(connection.state.row)
	connection.state.mutex.Unlock()
	transaction := &postgresqlStructuredTestTransaction{connection: connection, row: row}
	connection.active = transaction
	return transaction, nil
}

func (connection *postgresqlStructuredTestConnection) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	if connection.active == nil {
		return nil, errors.New("query outside transaction")
	}
	if strings.Contains(query, "current_setting('synchronous_commit')") {
		return structuredPostgreSQLAuthorityRows(connection.state.authority), nil
	}
	if strings.Contains(query, "to_regclass('heteroci_receipt.terminal_receipts')") {
		return structuredPostgreSQLObjectRows(connection.state.authority), nil
	}
	if strings.Contains(query, "pg_advisory_xact_lock") {
		return structuredPostgreSQLLockRows(), nil
	}
	if strings.Contains(query, "FROM heteroci_receipt.receipt_events") {
		return structuredPostgreSQLEventRows(connection.active.row.events, arguments, query), nil
	}
	if strings.Contains(query, "FROM heteroci_receipt.terminal_receipts") {
		return structuredPostgreSQLTerminalRows(connection.active.row.receipt, arguments), nil
	}
	return nil, fmt.Errorf("unexpected structured receipt query: %s", query)
}

func (connection *postgresqlStructuredTestConnection) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	if connection.active == nil {
		return nil, errors.New("exec outside transaction")
	}
	if strings.Contains(query, "INSERT INTO heteroci_receipt.receipt_events") {
		return connection.active.insertEvent(arguments)
	}
	if strings.Contains(query, "INSERT INTO heteroci_receipt.terminal_receipts") {
		return connection.active.insertTerminal(arguments)
	}
	return nil, fmt.Errorf("unexpected structured receipt exec: %s", query)
}

type postgresqlStructuredTestTransaction struct {
	connection *postgresqlStructuredTestConnection
	row        postgresqlStructuredTestRow
}

func (transaction *postgresqlStructuredTestTransaction) Commit() error {
	state := transaction.connection.state
	state.mutex.Lock()
	state.row = clonePostgreSQLStructuredTestRow(transaction.row)
	err := state.nextCommitError
	state.nextCommitError = nil
	state.mutex.Unlock()
	transaction.connection.active = nil
	return err
}

func (transaction *postgresqlStructuredTestTransaction) Rollback() error {
	transaction.connection.active = nil
	return nil
}

func (transaction *postgresqlStructuredTestTransaction) insertEvent(arguments []driver.NamedValue) (driver.Result, error) {
	seq := testInt64Argument(arguments, 7)
	if _, found := transaction.row.events[seq]; found {
		return driver.RowsAffected(0), nil
	}
	transaction.row.events[seq] = &postgresqlReceiptEventRow{
		schemaVersion: testStringArgument(arguments, 0), operationID: testStringArgument(arguments, 1),
		manifestDigest: testStringArgument(arguments, 2), planDigest: testStringArgument(arguments, 3),
		nodeID: testStringArgument(arguments, 4), executorKind: testStringArgument(arguments, 5),
		authorityGeneration: testInt64Argument(arguments, 6), seq: seq, occurredAt: testStringArgument(arguments, 8),
		stage: testStringArgument(arguments, 9), state: testStringArgument(arguments, 10),
		nodeIDDigest: testStringArgument(arguments, 11), code: testStringArgument(arguments, 12),
		eventBytes: bytes.Clone(arguments[13].Value.([]byte)), eventDigest: testStringArgument(arguments, 14),
	}
	return driver.RowsAffected(1), nil
}

func (transaction *postgresqlStructuredTestTransaction) insertTerminal(arguments []driver.NamedValue) (driver.Result, error) {
	if transaction.row.receipt != nil {
		return driver.RowsAffected(0), nil
	}
	transaction.row.receipt = &postgresqlTerminalReceiptRow{
		schemaVersion: testStringArgument(arguments, 0), operationID: testStringArgument(arguments, 1),
		requestDigest: testStringArgument(arguments, 2), manifestDigest: testStringArgument(arguments, 3),
		planDigest: testStringArgument(arguments, 4), nodeID: testStringArgument(arguments, 5),
		executorKind: testStringArgument(arguments, 6), authorityGeneration: testInt64Argument(arguments, 7),
		startedAt: testStringArgument(arguments, 8), finishedAt: testStringArgument(arguments, 9),
		exitCode: int(testInt64Argument(arguments, 10)), cleanupState: testStringArgument(arguments, 11),
		errorCode: testStringArgument(arguments, 12), capacityCode: testStringArgument(arguments, 13),
		status: testStringArgument(arguments, 14), eventSeq: testInt64Argument(arguments, 15),
		eventDigest: testStringArgument(arguments, 16), receiptBytes: bytes.Clone(arguments[17].Value.([]byte)), receiptDigest: testStringArgument(arguments, 18),
	}
	return driver.RowsAffected(1), nil
}

func testInt64Argument(arguments []driver.NamedValue, index int) int64 {
	value, ok := arguments[index].Value.(int64)
	if !ok {
		return int64(arguments[index].Value.(int))
	}
	return value
}

func structuredPostgreSQLAuthorityRows(authority postgresqlStructuredTestAuthority) driver.Rows {
	columns := []string{"current_user", "transaction_read_only", "synchronous_commit", "in_recovery", "server_address", "table_exists", "schema_owner", "table_owner"}
	values := []driver.Value{authority.currentUser, authority.readOnly, authority.synchronousCommit, authority.inRecovery, authority.serverAddress, authority.tableExists, authority.schemaOwner, PostgreSQLReceiptWriterRole}
	return &postgresqlStructuredTestRows{columns: columns, rows: [][]driver.Value{values}}
}

func structuredPostgreSQLObjectRows(authority postgresqlStructuredTestAuthority) driver.Rows {
	columns := []string{"terminal_exists", "event_exists", "schema_owner", "table_ownership", "index_names"}
	values := []driver.Value{authority.tableExists, authority.eventTableExists, authority.schemaOwner, authority.tableOwnership, authority.indexNames}
	return &postgresqlStructuredTestRows{columns: columns, rows: [][]driver.Value{values}}
}

func structuredPostgreSQLLockRows() driver.Rows {
	return &postgresqlStructuredTestRows{columns: []string{"pg_advisory_xact_lock"}, rows: [][]driver.Value{{nil}}}
}

func structuredPostgreSQLEventRows(events map[int64]*postgresqlReceiptEventRow, arguments []driver.NamedValue, query string) driver.Rows {
	operationID := testStringArgument(arguments, 0)
	if strings.Contains(query, "ORDER BY seq ASC") {
		return structuredPostgreSQLEventRowsAscending(events, operationID)
	}
	if strings.Contains(query, "ORDER BY seq DESC") {
		return structuredPostgreSQLEventRowsHead(events, operationID)
	}
	return structuredPostgreSQLEventRowsBySequence(events, arguments, operationID)
}

func structuredPostgreSQLEventRowsAscending(events map[int64]*postgresqlReceiptEventRow, operationID string) driver.Rows {
	keys := make([]int64, 0, len(events))
	for seq, event := range events {
		if event.operationID == operationID {
			keys = append(keys, seq)
		}
	}
	slices.Sort(keys)
	values := make([][]driver.Value, 0, len(keys))
	for _, seq := range keys {
		values = append(values, structuredPostgreSQLEventValues(events[seq]))
	}
	return &postgresqlStructuredTestRows{columns: structuredPostgreSQLEventColumns(), rows: values}
}

func structuredPostgreSQLEventRowsHead(events map[int64]*postgresqlReceiptEventRow, operationID string) driver.Rows {
	var selected *postgresqlReceiptEventRow
	for _, event := range events {
		if event.operationID == operationID && (selected == nil || event.seq > selected.seq) {
			selected = event
		}
	}
	return structuredPostgreSQLEventRowsFor(selected)
}

func structuredPostgreSQLEventRowsBySequence(events map[int64]*postgresqlReceiptEventRow, arguments []driver.NamedValue, operationID string) driver.Rows {
	var selected *postgresqlReceiptEventRow
	if len(arguments) > 1 {
		candidate := events[testInt64Argument(arguments, 1)]
		if candidate != nil && candidate.operationID == operationID {
			selected = candidate
		}
	}
	return structuredPostgreSQLEventRowsFor(selected)
}

func structuredPostgreSQLEventRowsFor(event *postgresqlReceiptEventRow) driver.Rows {
	columns := structuredPostgreSQLEventColumns()
	if event == nil {
		return &postgresqlStructuredTestRows{columns: columns}
	}
	return &postgresqlStructuredTestRows{columns: columns, rows: [][]driver.Value{structuredPostgreSQLEventValues(event)}}
}

func structuredPostgreSQLEventColumns() []string {
	return []string{"schema_version", "operation_id", "manifest_digest", "plan_digest", "node_id", "executor_kind", "authority_generation", "seq", "occurred_at", "stage", "state", "node_id_digest", "code", "event_bytes", "event_digest"}
}

func structuredPostgreSQLEventValues(event *postgresqlReceiptEventRow) []driver.Value {
	return []driver.Value{event.schemaVersion, event.operationID, event.manifestDigest, event.planDigest, event.nodeID, event.executorKind, event.authorityGeneration, event.seq, event.occurredAt, event.stage, event.state, event.nodeIDDigest, event.code, bytes.Clone(event.eventBytes), event.eventDigest}
}

func structuredPostgreSQLTerminalRows(receipt *postgresqlTerminalReceiptRow, arguments []driver.NamedValue) driver.Rows {
	columns := []string{"schema_version", "operation_id", "request_digest", "manifest_digest", "plan_digest", "node_id", "executor_kind", "authority_generation", "started_at", "finished_at", "exit_code", "cleanup_state", "error_code", "capacity_code", "status", "event_seq", "event_digest", "receipt_bytes", "receipt_digest"}
	if receipt == nil || receipt.operationID != testStringArgument(arguments, 0) {
		return &postgresqlStructuredTestRows{columns: columns}
	}
	values := []driver.Value{receipt.schemaVersion, receipt.operationID, receipt.requestDigest, receipt.manifestDigest, receipt.planDigest, receipt.nodeID, receipt.executorKind, receipt.authorityGeneration, receipt.startedAt, receipt.finishedAt, int64(receipt.exitCode), receipt.cleanupState, receipt.errorCode, receipt.capacityCode, receipt.status, receipt.eventSeq, receipt.eventDigest, bytes.Clone(receipt.receiptBytes), receipt.receiptDigest}
	return &postgresqlStructuredTestRows{columns: columns, rows: [][]driver.Value{values}}
}

type postgresqlStructuredTestRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *postgresqlStructuredTestRows) Columns() []string { return rows.columns }
func (rows *postgresqlStructuredTestRows) Close() error      { return nil }
func (rows *postgresqlStructuredTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func openPostgreSQLStructuredTestStore(t *testing.T, state *postgresqlStructuredTestState) (*PostgreSQLReceiptStore, *sql.DB) {
	t.Helper()
	database := sql.OpenDB(postgresqlStructuredTestConnector{state: state})
	database.SetMaxOpenConns(1)
	return &PostgreSQLReceiptStore{database: database}, database
}

func closePostgreSQLStructuredTestDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Errorf("close PostgreSQL structured test database: %v", err)
	}
}

func assertPostgreSQLStructuredCanonicalBytes(t *testing.T, state *postgresqlStructuredTestState, receipt PostgreSQLTerminalReceipt, events []PostgreSQLReceiptEvent) {
	t.Helper()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.row.receipt == nil {
		t.Fatal("terminal receipt was not persisted")
	}
	receiptBytes, receiptDigest, err := canonicalPostgreSQLTerminalReceipt(receipt)
	if err != nil || !bytes.Equal(state.row.receipt.receiptBytes, receiptBytes) || state.row.receipt.receiptDigest != receiptDigest {
		t.Fatalf("terminal canonical bytes = %q/%q, want %q/%q, err=%v", state.row.receipt.receiptBytes, state.row.receipt.receiptDigest, receiptBytes, receiptDigest, err)
	}
	for _, event := range events {
		persisted := state.row.events[event.Seq]
		if persisted == nil || persisted.eventDigest != event.EventDigest {
			t.Fatalf("event %d was not persisted exactly: %#v", event.Seq, persisted)
		}
	}
}
