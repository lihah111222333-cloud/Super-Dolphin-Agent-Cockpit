package heterociowner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

type postgresqlOwnerTestData struct {
	head      []driver.Value
	operation []driver.Value
}

func (data postgresqlOwnerTestData) clone() postgresqlOwnerTestData {
	return postgresqlOwnerTestData{head: append([]driver.Value(nil), data.head...), operation: append([]driver.Value(nil), data.operation...)}
}

type postgresqlOwnerTestState struct {
	mu                   sync.Mutex
	inRecovery           bool
	readOnly             string
	synchronousCommit    string
	serverAddress        string
	currentUser          string
	schemaOwner          string
	schemaVersion        string
	tableOwners          map[string]string
	data                 postgresqlOwnerTestData
	commitAppliedError   error
	serializableBegins   int
	advisoryLocks        int
	authorityBeforeLock  bool
	schemaStatementCount int
}

type postgresqlOwnerTestDriver struct{ state *postgresqlOwnerTestState }

func (testDriver *postgresqlOwnerTestDriver) Open(string) (driver.Conn, error) {
	return &postgresqlOwnerTestConn{state: testDriver.state}, nil
}

type postgresqlOwnerTestConn struct {
	state  *postgresqlOwnerTestState
	txData *postgresqlOwnerTestData
	locked bool
}

func (conn *postgresqlOwnerTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (conn *postgresqlOwnerTestConn) Close() error { return nil }
func (conn *postgresqlOwnerTestConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{Isolation: driver.IsolationLevel(sql.LevelSerializable)})
}

func (conn *postgresqlOwnerTestConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	if conn.txData != nil {
		return nil, errors.New("transaction is already active")
	}
	if options.Isolation != driver.IsolationLevel(sql.LevelSerializable) {
		return nil, fmt.Errorf("isolation = %d", options.Isolation)
	}
	data := conn.state.data.clone()
	conn.txData, conn.locked = &data, false
	conn.state.serializableBegins++
	return (*postgresqlOwnerTestTx)(conn), nil
}

func (conn *postgresqlOwnerTestConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	if rows, handled := conn.queryAuthorityMetadata(query); handled {
		return rows, nil
	}
	if rows, handled := conn.queryOwnerHeadUpsert(query, args); handled {
		return rows, nil
	}
	switch {
	case strings.Contains(query, "pg_advisory_xact_lock"):
		conn.locked = true
		conn.state.advisoryLocks++
		return newPostgreSQLOwnerTestRows([]string{"lock"}, [][]driver.Value{{nil}}), nil
	case strings.Contains(query, "FROM heteroci_owner.owner_heads") && strings.Contains(query, "owner_id"):
		conn.noteAuthorityRead()
		data := conn.currentData()
		if len(data.head) == 0 {
			return newPostgreSQLOwnerTestRows([]string{"owner_id", "owner_epoch", "revision"}, nil), nil
		}
		return newPostgreSQLOwnerTestRows([]string{"owner_id", "owner_epoch", "revision"}, [][]driver.Value{{data.head[0], data.head[1], data.head[2]}}), nil
	case strings.Contains(query, "FROM heteroci_owner.operation_journal"):
		conn.noteAuthorityRead()
		data := conn.currentData()
		return newPostgreSQLOwnerTestRows([]string{"owner_id", "owner_epoch", "revision", "kind", "request_bytes", "request_digest", "result_bytes", "result_digest", "previous_journal_digest", "journal_digest", "projection_digest", "committed_at_unix_milli"}, valuesAsRows(data.operation)), nil
	case strings.Contains(query, "FROM heteroci_owner.routing_reservations"):
		conn.noteAuthorityRead()
		return newPostgreSQLOwnerTestRows([]string{"node_id", "probe_identity", "probe_boot_id", "cpu_milli", "memory_bytes", "max_concurrency"}, nil), nil
	case strings.Contains(query, "SELECT journal_digest FROM heteroci_owner.owner_heads"):
		conn.noteAuthorityRead()
		data := conn.currentData()
		return newPostgreSQLOwnerTestRows([]string{"journal_digest"}, [][]driver.Value{{data.head[3]}}), nil
	default:
		return nil, fmt.Errorf("unexpected PostgreSQL owner query: %s (%d args)", query, len(args))
	}
}

func (conn *postgresqlOwnerTestConn) queryOwnerHeadUpsert(query string, args []driver.NamedValue) (driver.Rows, bool) {
	if !strings.HasPrefix(query, "INSERT INTO heteroci_owner.owner_heads") {
		return nil, false
	}
	data := conn.currentData()
	epoch, revision, journal := int64(1), int64(1), driver.Value(zeroDigest)
	if len(data.head) != 0 {
		epoch, revision, journal = data.head[1].(int64)+1, data.head[2].(int64)+1, data.head[3]
	}
	data.head = []driver.Value{args[1].Value, epoch, revision, journal, args[2].Value, args[3].Value}
	return newPostgreSQLOwnerTestRows([]string{"owner_epoch", "revision"}, [][]driver.Value{{epoch, revision}}), true
}

func (conn *postgresqlOwnerTestConn) queryAuthorityMetadata(query string) (driver.Rows, bool) {
	switch {
	case strings.Contains(query, "pg_is_in_recovery"):
		return newPostgreSQLOwnerTestRows([]string{"in_recovery", "transaction_read_only", "synchronous_commit", "server_address", "current_user"}, [][]driver.Value{{conn.state.inRecovery, conn.state.readOnly, conn.state.synchronousCommit, conn.state.serverAddress, conn.state.currentUser}}), true
	case strings.Contains(query, "string_agg"):
		if conn.state.schemaOwner == "" {
			return newPostgreSQLOwnerTestRows([]string{"schema_owner", "table_ownership"}, nil), true
		}
		parts := make([]string, 0, len(conn.state.tableOwners))
		for _, name := range postgresqlOwnerTableNames() {
			if owner, ok := conn.state.tableOwners[name]; ok {
				parts = append(parts, name+":"+owner)
			}
		}
		return newPostgreSQLOwnerTestRows([]string{"schema_owner", "schema_version", "table_ownership"}, [][]driver.Value{{conn.state.schemaOwner, conn.state.schemaVersion, strings.Join(parts, ",")}}), true
	default:
		return nil, false
	}
}

func (conn *postgresqlOwnerTestConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	if conn.txData == nil {
		if strings.HasPrefix(query, "CREATE ") {
			conn.state.schemaStatementCount++
			return driver.RowsAffected(0), nil
		}
		return nil, fmt.Errorf("unexpected PostgreSQL owner non-transaction exec: %s", query)
	}
	data := conn.currentData()
	switch {
	case strings.HasPrefix(query, "UPDATE heteroci_owner.owner_heads SET revision"):
		data.head[2], data.head[3], data.head[4], data.head[5] = args[0].Value, args[1].Value, args[2].Value, args[3].Value
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.operation_journal"):
		data.operation = namedValues(args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11], args[12], args[13])
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.owner_epochs"), strings.HasPrefix(query, "INSERT INTO heteroci_owner.routing_"), strings.HasPrefix(query, "UPDATE heteroci_owner.owner_epochs"):
	default:
		return nil, fmt.Errorf("unexpected PostgreSQL owner exec: %s", query)
	}
	return driver.RowsAffected(1), nil
}

func (conn *postgresqlOwnerTestConn) noteAuthorityRead() {
	if conn.txData == nil || !conn.locked {
		conn.state.authorityBeforeLock = true
	}
}
func (conn *postgresqlOwnerTestConn) currentData() *postgresqlOwnerTestData {
	if conn.txData != nil {
		return conn.txData
	}
	return &conn.state.data
}

type postgresqlOwnerTestTx postgresqlOwnerTestConn

func (tx *postgresqlOwnerTestTx) Commit() error {
	conn := (*postgresqlOwnerTestConn)(tx)
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	conn.state.data = conn.txData.clone()
	conn.txData, conn.locked = nil, false
	err := conn.state.commitAppliedError
	conn.state.commitAppliedError = nil
	return err
}
func (tx *postgresqlOwnerTestTx) Rollback() error {
	conn := (*postgresqlOwnerTestConn)(tx)
	conn.state.mu.Lock()
	defer conn.state.mu.Unlock()
	conn.txData, conn.locked = nil, false
	return nil
}

type postgresqlOwnerTestRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func newPostgreSQLOwnerTestRows(columns []string, rows [][]driver.Value) driver.Rows {
	return &postgresqlOwnerTestRows{columns: columns, rows: rows}
}
func (rows *postgresqlOwnerTestRows) Columns() []string { return rows.columns }
func (rows *postgresqlOwnerTestRows) Close() error      { return nil }
func (rows *postgresqlOwnerTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func namedValues(values ...driver.NamedValue) []driver.Value {
	result := make([]driver.Value, len(values))
	for index := range values {
		result[index] = values[index].Value
	}
	return result
}
func valuesAsRows(values []driver.Value) [][]driver.Value {
	if len(values) == 0 {
		return nil
	}
	return [][]driver.Value{values}
}

func openPostgreSQLOwnerTestDB(t *testing.T) (*sql.DB, *postgresqlOwnerTestState) {
	t.Helper()
	state := &postgresqlOwnerTestState{readOnly: "off", synchronousCommit: PostgreSQLOwnerSynchronousCommitRemoteApply, serverAddress: "203.0.113.10", currentUser: PostgreSQLOwnerRole, schemaOwner: PostgreSQLOwnerRole, schemaVersion: PostgreSQLOwnerRuntimeSchemaVersion, tableOwners: make(map[string]string)}
	for _, name := range postgresqlOwnerTableNames() {
		state.tableOwners[name] = PostgreSQLOwnerRole
	}
	name := "heteroci-postgresql-owner-test-" + strings.ReplaceAll(t.Name(), "/", "-")
	sql.Register(name, &postgresqlOwnerTestDriver{state: state})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("open PostgreSQL owner test database: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close PostgreSQL owner test database: %v", err)
		}
	})
	return db, state
}
