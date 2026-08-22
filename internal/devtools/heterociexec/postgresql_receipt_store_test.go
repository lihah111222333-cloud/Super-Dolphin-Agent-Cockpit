package heterociexec

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"
)

func TestPostgreSQLReceiptStore(t *testing.T) {
	t.Run("contract", testPostgreSQLReceiptContract)
	t.Run("direct_writer_login", testPostgreSQLReceiptDirectWriterLogin)
	t.Run("authority", testPostgreSQLReceiptAuthority)
	t.Run("claim_terminal", testPostgreSQLReceiptClaimTerminal)
	t.Run("ambiguous_commit", testPostgreSQLReceiptAmbiguousCommit)
}

func testPostgreSQLReceiptContract(t *testing.T) {
	if PostgreSQLReceiptSchema != "heteroci_receipt" || PostgreSQLReceiptWriterRole != "heteroci_receipt_writer" {
		t.Fatalf("receipt authority = %q/%q", PostgreSQLReceiptSchema, PostgreSQLReceiptWriterRole)
	}
	assertPostgreSQLReceiptRoleDDL(t)
	for _, fragment := range []string{
		"CREATE SCHEMA heteroci_receipt AUTHORIZATION heteroci_receipt_writer",
		"CREATE TABLE heteroci_receipt.batch_receipts",
		"claim_bytes BYTEA NOT NULL",
		"result_bytes BYTEA",
		"PRIMARY KEY (batch_id, material_digest)",
		"REVOKE ALL ON TABLE heteroci_receipt.batch_receipts FROM PUBLIC",
	} {
		if !strings.Contains(PostgreSQLReceiptDDL, fragment) {
			t.Fatalf("PostgreSQLReceiptDDL lacks %q", fragment)
		}
	}
	if strings.Contains(strings.ToLower(PostgreSQLReceiptDDL), "sqlite") {
		t.Fatal("PostgreSQLReceiptDDL contains SQLite")
	}
	assertPostgreSQLReceiptPrimaryKeyCoverage(t)
	fields := reflect.VisibleFields(reflect.TypeFor[PostgreSQLReceiptStore]())
	if len(fields) != 1 || fields[0].Name != "database" || fields[0].Type != reflect.TypeFor[*sql.DB]() {
		t.Fatalf("PostgreSQLReceiptStore fields = %#v", fields)
	}
	if _, err := NewPostgreSQLReceiptStore(context.Background(), nil); !errors.Is(err, ErrPostgreSQLReceiptStore) {
		t.Fatalf("NewPostgreSQLReceiptStore(nil) error = %v", err)
	}
}

func assertPostgreSQLReceiptPrimaryKeyCoverage(t *testing.T) {
	t.Helper()
	if strings.Contains(PostgreSQLReceiptDDL, "CREATE INDEX") && strings.Contains(PostgreSQLReceiptDDL, "ON heteroci_receipt.batch_receipts") {
		t.Fatal("PostgreSQLReceiptDDL adds an index redundant with batch_receipts primary key")
	}
	assertPostgreSQLReceiptPrimaryKeyQuery(t, "read", selectPostgreSQLReceiptRow)
	assertPostgreSQLReceiptPrimaryKeyQuery(t, "update", updatePostgreSQLTerminalReceipt)
}

func assertPostgreSQLReceiptPrimaryKeyQuery(t *testing.T, name, query string) {
	t.Helper()
	if !strings.Contains(query, "WHERE batch_id = $") || !strings.Contains(query, "AND material_digest = $") {
		t.Fatalf("PostgreSQL receipt %s query does not bind the complete primary key: %s", name, query)
	}
}

func assertPostgreSQLReceiptRoleDDL(t *testing.T) {
	t.Helper()
	const roleDDL = "CREATE ROLE heteroci_receipt_writer\n    LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;"
	if !strings.Contains(PostgreSQLReceiptDDL, roleDDL) {
		t.Fatalf("PostgreSQLReceiptDDL lacks direct-login least-privilege role %q", roleDDL)
	}
	for _, forbidden := range []string{"NOLOGIN", "PASSWORD", "SET ROLE"} {
		if strings.Contains(PostgreSQLReceiptDDL, forbidden) {
			t.Fatalf("PostgreSQLReceiptDDL contains forbidden deployment shape %q", forbidden)
		}
	}
}

func testPostgreSQLReceiptDirectWriterLogin(t *testing.T) {
	state := &postgreSQLTestState{authority: validPostgreSQLTestAuthority("10.20.30.40")}
	store, database := openPostgreSQLTestStore(t, state)
	defer closePostgreSQLTestDatabase(t, database)
	if store == nil {
		t.Fatal("direct heteroci_receipt_writer login did not construct a receipt store")
	}
}

func testPostgreSQLReceiptAuthority(t *testing.T) {
	cases := []struct {
		name      string
		authority postgreSQLTestAuthority
	}{
		{name: "loopback", authority: validPostgreSQLTestAuthority("127.0.0.1")},
		{name: "standby", authority: mutatePostgreSQLTestAuthority(func(value *postgreSQLTestAuthority) { value.inRecovery = true })},
		{name: "read_only", authority: mutatePostgreSQLTestAuthority(func(value *postgreSQLTestAuthority) { value.readOnly = "on" })},
		{name: "async", authority: mutatePostgreSQLTestAuthority(func(value *postgreSQLTestAuthority) { value.synchronousCommit = "on" })},
		{name: "wrong_role", authority: mutatePostgreSQLTestAuthority(func(value *postgreSQLTestAuthority) { value.currentUser = "heteroci_owner_writer" })},
		{name: "missing_table", authority: mutatePostgreSQLTestAuthority(func(value *postgreSQLTestAuthority) { value.tableExists = false })},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state := &postgreSQLTestState{authority: test.authority}
			database := openPostgreSQLTestDatabase(t, state)
			defer closePostgreSQLTestDatabase(t, database)
			if _, err := NewPostgreSQLReceiptStore(context.Background(), database); !errors.Is(err, ErrPostgreSQLReceiptStore) {
				t.Fatalf("NewPostgreSQLReceiptStore() error = %v", err)
			}
		})
	}
}

func testPostgreSQLReceiptClaimTerminal(t *testing.T) {
	state := &postgreSQLTestState{authority: validPostgreSQLTestAuthority("10.20.30.40")}
	store, database := openPostgreSQLTestStore(t, state)
	defer closePostgreSQLTestDatabase(t, database)
	batch := commandExecutorBatch()
	commandDigest := commandExecutorCommandDigest(t, []string{"tool"})
	claimed, err := store.Claim(context.Background(), batch, commandDigest)
	if err != nil || !claimed {
		t.Fatalf("Claim() = %t, %v", claimed, err)
	}
	claimed, err = store.Claim(context.Background(), batch, commandDigest)
	if err != nil || claimed {
		t.Fatalf("replayed Claim() = %t, %v", claimed, err)
	}
	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	if err := store.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("WriteIfAbsent() error = %v", err)
	}
	got, found, err := store.Read(context.Background(), batch, commandDigest)
	if err != nil || !found || !reflect.DeepEqual(got, result) {
		t.Fatalf("Read() = %#v, %t, %v", got, found, err)
	}
	assertPostgreSQLReceiptConflicts(t, store, batch)
	assertPostgreSQLTestCanonicalBytes(t, state)
}

func assertPostgreSQLReceiptConflicts(t *testing.T, store *PostgreSQLReceiptStore, batch heterociworkflow.ExecutionBatch) {
	t.Helper()
	driftedCommand := commandExecutorCommandDigest(t, []string{"other-tool"})
	if _, err := store.Claim(context.Background(), batch, driftedCommand); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("drifted Claim() error = %v", err)
	}
	conflict := canonicalBatchResult(batch, heteroci.LeaseStateFailed)
	if err := store.WriteIfAbsent(context.Background(), conflict); !errors.Is(err, ErrPostgreSQLReceiptConflict) {
		t.Fatalf("conflicting WriteIfAbsent() error = %v", err)
	}
}

func testPostgreSQLReceiptAmbiguousCommit(t *testing.T) {
	state := &postgreSQLTestState{authority: validPostgreSQLTestAuthority("10.20.30.40")}
	store, database := openPostgreSQLTestStore(t, state)
	defer closePostgreSQLTestDatabase(t, database)
	batch := commandExecutorBatch()
	commandDigest := commandExecutorCommandDigest(t, []string{"tool"})
	state.setCommitError(errors.New("claim commit acknowledgement lost"))
	claimed, err := store.Claim(context.Background(), batch, commandDigest)
	if err != nil || !claimed {
		t.Fatalf("ambiguous Claim() = %t, %v", claimed, err)
	}
	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	state.setCommitError(errors.New("terminal commit acknowledgement lost"))
	if err := store.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("ambiguous WriteIfAbsent() error = %v", err)
	}
	assertPostgreSQLTestCanonicalBytes(t, state)
}

func assertPostgreSQLTestCanonicalBytes(t *testing.T, state *postgreSQLTestState) {
	t.Helper()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.row == nil || state.row.state != canonicalReceiptStateTerminal {
		t.Fatal("terminal row was not persisted")
	}
	for _, producer := range []struct {
		typeOf reflect.Type
		raw    []byte
	}{{typeOf: reflect.TypeFor[canonicalReceiptClaim](), raw: state.row.claimBytes}, {typeOf: reflect.TypeFor[heterociworkflow.BatchResult](), raw: state.row.resultBytes}} {
		for _, field := range reflect.VisibleFields(producer.typeOf) {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "-" || !bytes.Contains(producer.raw, []byte(`"`+name+`"`)) {
				t.Fatalf("canonical BYTEA omitted %s.%s", producer.typeOf.Name(), field.Name)
			}
		}
	}
}

type postgreSQLTestAuthority struct {
	currentUser       string
	readOnly          string
	synchronousCommit string
	inRecovery        bool
	serverAddress     string
	tableExists       bool
	schemaOwner       string
	tableOwner        string
}

func validPostgreSQLTestAuthority(address string) postgreSQLTestAuthority {
	return postgreSQLTestAuthority{currentUser: PostgreSQLReceiptWriterRole, readOnly: "off", synchronousCommit: "remote_apply", serverAddress: address, tableExists: true, schemaOwner: PostgreSQLReceiptWriterRole, tableOwner: PostgreSQLReceiptWriterRole}
}

func mutatePostgreSQLTestAuthority(mutate func(*postgreSQLTestAuthority)) postgreSQLTestAuthority {
	value := validPostgreSQLTestAuthority("10.20.30.40")
	mutate(&value)
	return value
}

type postgreSQLTestRow struct {
	schemaVersion  string
	batchID        string
	materialDigest string
	commandDigest  string
	state          string
	claimBytes     []byte
	claimDigest    string
	resultBytes    []byte
	resultDigest   any
}

func clonePostgreSQLTestRow(row *postgreSQLTestRow) *postgreSQLTestRow {
	if row == nil {
		return nil
	}
	clone := *row
	clone.claimBytes = bytes.Clone(row.claimBytes)
	clone.resultBytes = bytes.Clone(row.resultBytes)
	return &clone
}

type postgreSQLTestState struct {
	mutex           sync.Mutex
	authority       postgreSQLTestAuthority
	row             *postgreSQLTestRow
	nextCommitError error
}

func (state *postgreSQLTestState) setCommitError(err error) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.nextCommitError = err
}

type postgreSQLTestConnector struct{ state *postgreSQLTestState }

func (connector postgreSQLTestConnector) Connect(context.Context) (driver.Conn, error) {
	return &postgreSQLTestConnection{state: connector.state}, nil
}

func (connector postgreSQLTestConnector) Driver() driver.Driver { return postgreSQLTestDriver{} }

type postgreSQLTestDriver struct{}

func (postgreSQLTestDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("test driver requires Connector")
}

type postgreSQLTestConnection struct {
	state  *postgreSQLTestState
	active *postgreSQLTestTransaction
}

func (connection *postgreSQLTestConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are forbidden")
}

func (connection *postgreSQLTestConnection) Close() error { return nil }
func (connection *postgreSQLTestConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transaction options are required")
}

func (connection *postgreSQLTestConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelSerializable) || options.ReadOnly || connection.active != nil {
		return nil, fmt.Errorf("invalid transaction options: %#v", options)
	}
	connection.state.mutex.Lock()
	row := clonePostgreSQLTestRow(connection.state.row)
	connection.state.mutex.Unlock()
	transaction := &postgreSQLTestTransaction{connection: connection, row: row}
	connection.active = transaction
	return transaction, nil
}

func (connection *postgreSQLTestConnection) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	if connection.active == nil {
		return nil, errors.New("query outside transaction")
	}
	if strings.Contains(query, "current_setting('synchronous_commit')") {
		return postgreSQLAuthorityRows(connection.state.authority), nil
	}
	if !strings.Contains(query, "FROM heteroci_receipt.batch_receipts") {
		return nil, fmt.Errorf("unexpected query: %s", query)
	}
	return postgreSQLReceiptRows(connection.active.row, arguments), nil
}

func (connection *postgreSQLTestConnection) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	if connection.active == nil {
		return nil, errors.New("exec outside transaction")
	}
	if strings.Contains(query, "INSERT INTO heteroci_receipt.batch_receipts") {
		return connection.active.insertClaim(arguments)
	}
	if strings.Contains(query, "UPDATE heteroci_receipt.batch_receipts") {
		return connection.active.writeTerminal(arguments)
	}
	return nil, fmt.Errorf("unexpected exec: %s", query)
}

type postgreSQLTestTransaction struct {
	connection *postgreSQLTestConnection
	row        *postgreSQLTestRow
}

func (transaction *postgreSQLTestTransaction) Commit() error {
	state := transaction.connection.state
	state.mutex.Lock()
	state.row = clonePostgreSQLTestRow(transaction.row)
	err := state.nextCommitError
	state.nextCommitError = nil
	state.mutex.Unlock()
	transaction.connection.active = nil
	return err
}

func (transaction *postgreSQLTestTransaction) Rollback() error {
	transaction.connection.active = nil
	return nil
}

func (transaction *postgreSQLTestTransaction) insertClaim(arguments []driver.NamedValue) (driver.Result, error) {
	if transaction.row != nil {
		return driver.RowsAffected(0), nil
	}
	transaction.row = &postgreSQLTestRow{schemaVersion: testStringArgument(arguments, 0), batchID: testStringArgument(arguments, 1), materialDigest: testStringArgument(arguments, 2), commandDigest: testStringArgument(arguments, 3), state: testStringArgument(arguments, 4), claimBytes: bytes.Clone(arguments[5].Value.([]byte)), claimDigest: testStringArgument(arguments, 6)}
	return driver.RowsAffected(1), nil
}

func (transaction *postgreSQLTestTransaction) writeTerminal(arguments []driver.NamedValue) (driver.Result, error) {
	if transaction.row == nil || transaction.row.state != canonicalReceiptStateClaimed || transaction.row.commandDigest != testStringArgument(arguments, 5) {
		return driver.RowsAffected(0), nil
	}
	transaction.row.state = testStringArgument(arguments, 0)
	transaction.row.resultBytes = bytes.Clone(arguments[1].Value.([]byte))
	transaction.row.resultDigest = testStringArgument(arguments, 2)
	return driver.RowsAffected(1), nil
}

func testStringArgument(arguments []driver.NamedValue, index int) string {
	value, _ := arguments[index].Value.(string)
	return value
}

func postgreSQLAuthorityRows(authority postgreSQLTestAuthority) driver.Rows {
	columns := []string{"current_user", "transaction_read_only", "synchronous_commit", "in_recovery", "server_address", "table_exists", "schema_owner", "table_owner"}
	values := []driver.Value{authority.currentUser, authority.readOnly, authority.synchronousCommit, authority.inRecovery, authority.serverAddress, authority.tableExists, authority.schemaOwner, authority.tableOwner}
	return &postgreSQLTestRows{columns: columns, rows: [][]driver.Value{values}}
}

func postgreSQLReceiptRows(row *postgreSQLTestRow, arguments []driver.NamedValue) driver.Rows {
	columns := []string{"schema_version", "command_digest", "state", "claim_bytes", "claim_digest", "result_bytes", "result_digest"}
	if row == nil || row.batchID != testStringArgument(arguments, 0) || row.materialDigest != testStringArgument(arguments, 1) {
		return &postgreSQLTestRows{columns: columns}
	}
	values := []driver.Value{row.schemaVersion, row.commandDigest, row.state, bytes.Clone(row.claimBytes), row.claimDigest, bytes.Clone(row.resultBytes), row.resultDigest}
	return &postgreSQLTestRows{columns: columns, rows: [][]driver.Value{values}}
}

type postgreSQLTestRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (rows *postgreSQLTestRows) Columns() []string { return rows.columns }
func (rows *postgreSQLTestRows) Close() error      { return nil }
func (rows *postgreSQLTestRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(destination, rows.rows[rows.index])
	rows.index++
	return nil
}

func openPostgreSQLTestStore(t *testing.T, state *postgreSQLTestState) (*PostgreSQLReceiptStore, *sql.DB) {
	t.Helper()
	database := openPostgreSQLTestDatabase(t, state)
	store, err := NewPostgreSQLReceiptStore(context.Background(), database)
	if err != nil {
		closePostgreSQLTestDatabase(t, database)
		t.Fatalf("NewPostgreSQLReceiptStore() error = %v", err)
	}
	return store, database
}

func openPostgreSQLTestDatabase(t *testing.T, state *postgreSQLTestState) *sql.DB {
	t.Helper()
	database := sql.OpenDB(postgreSQLTestConnector{state: state})
	database.SetMaxOpenConns(1)
	return database
}

func closePostgreSQLTestDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	if err := database.Close(); err != nil {
		t.Errorf("close PostgreSQL test database: %v", err)
	}
}
