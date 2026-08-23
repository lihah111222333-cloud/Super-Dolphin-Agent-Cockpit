package heterociowner

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type postgresqlOwnerTestData struct {
	head         []driver.Value
	operations   map[string][]driver.Value
	epochs       map[int64][]driver.Value
	runs         map[string][]driver.Value
	leases       map[string][]driver.Value
	reservations map[string][]driver.Value
}

func (data postgresqlOwnerTestData) clone() postgresqlOwnerTestData {
	clone := postgresqlOwnerTestData{head: append([]driver.Value(nil), data.head...), operations: clonePostgreSQLDriverStringMap(data.operations), runs: clonePostgreSQLDriverStringMap(data.runs), leases: clonePostgreSQLDriverStringMap(data.leases), reservations: clonePostgreSQLDriverStringMap(data.reservations), epochs: clonePostgreSQLDriverEpochMap(data.epochs)}
	return clone
}

func clonePostgreSQLDriverStringMap(source map[string][]driver.Value) map[string][]driver.Value {
	clone := make(map[string][]driver.Value, len(source))
	for key, value := range source {
		clone[key] = append([]driver.Value(nil), value...)
	}
	return clone
}

func clonePostgreSQLDriverEpochMap(source map[int64][]driver.Value) map[int64][]driver.Value {
	clone := make(map[int64][]driver.Value, len(source))
	for key, value := range source {
		clone[key] = append([]driver.Value(nil), value...)
	}
	return clone
}

type postgresqlOwnerTestState struct {
	mu                           sync.Mutex
	inRecovery                   bool
	readOnly                     string
	synchronousCommit            string
	serverAddress                string
	currentUser                  string
	schemaOwner                  string
	schemaVersion                string
	tableOwners                  map[string]string
	indexNames                   map[string]struct{}
	data                         postgresqlOwnerTestData
	commitAppliedError           error
	rollbackError                error
	zeroReservationReleaseUpdate bool
	serializableBegins           int
	advisoryLocks                int
	authorityBeforeLock          bool
	schemaStatementCount         int
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
	if rows, handled := conn.queryLifecycleProjection(query, args); handled {
		return rows, nil
	}
	return conn.queryPostgreSQLBase(query, args)
}

func (conn *postgresqlOwnerTestConn) queryPostgreSQLBase(query string, args []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "pg_advisory_xact_lock"):
		conn.locked = true
		conn.state.advisoryLocks++
		return newPostgreSQLOwnerTestRows([]string{"lock"}, [][]driver.Value{{nil}}), nil
	case strings.Contains(query, "SELECT journal_digest, projection_digest FROM heteroci_owner.owner_heads"):
		conn.noteAuthorityRead()
		data := conn.currentData()
		return newPostgreSQLOwnerTestRows([]string{"journal_digest", "projection_digest"}, [][]driver.Value{{data.head[3], data.head[4]}}), nil
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
		operationID := fmt.Sprint(args[1].Value)
		return newPostgreSQLOwnerTestRows([]string{"owner_id", "owner_epoch", "revision", "kind", "request_bytes", "request_digest", "result_bytes", "result_digest", "previous_journal_digest", "journal_digest", "projection_digest", "committed_at_unix_milli", "external_binding_digest", "manifest_blob_sha256", "runtime_policy_digest"}, valuesAsRows(data.operations[operationID])), nil
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

func (conn *postgresqlOwnerTestConn) queryLifecycleProjection(query string, args []driver.NamedValue) (driver.Rows, bool) {
	data := conn.currentData()
	if rows, handled := queryPostgreSQLEpoch(query, args, data); handled {
		return rows, true
	}
	if rows, handled := queryPostgreSQLRun(query, args, data); handled {
		return rows, true
	}
	if rows, handled := queryPostgreSQLLease(query, args, data); handled {
		return rows, true
	}
	return queryPostgreSQLReservation(query, args, data)
}

func queryPostgreSQLEpoch(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (driver.Rows, bool) {
	if !strings.Contains(query, "FROM heteroci_owner.owner_epochs") {
		return nil, false
	}
	epoch := namedInt64(args[1].Value)
	return newPostgreSQLOwnerTestRows([]string{"owner_id", "started_at_unix_milli"}, valuesAsRows(data.epochs[epoch])), true
}

func queryPostgreSQLRun(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (driver.Rows, bool) {
	if !strings.Contains(query, "FROM heteroci_owner.routing_runs") {
		return nil, false
	}
	runID := fmt.Sprint(args[len(args)-1].Value)
	return newPostgreSQLOwnerTestRows([]string{"owner_epoch", "run_id", "repository_id", "source_tree_sha", "plan_operation_id", "heartbeat_ttl_milli", "last_revision"}, valuesAsRows(data.runs[runID])), true
}

func queryPostgreSQLLease(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (driver.Rows, bool) {
	if !strings.Contains(query, "FROM heteroci_owner.routing_leases") {
		return nil, false
	}
	if strings.Contains(query, "SELECT unit_id FROM") {
		return queryPostgreSQLLeaseIDs(args, data), true
	}
	if strings.Contains(query, "SELECT state, lease_bytes") {
		key := fmt.Sprintf("%s\x00%s", fmt.Sprint(args[2].Value), fmt.Sprint(args[3].Value))
		return newPostgreSQLOwnerTestRows([]string{"state", "lease_bytes", "started_bytes", "terminal_bytes", "last_revision", "started_at_unix_milli", "finished_at_unix_milli"}, valuesAsRows(data.leases[key])), true
	}
	if strings.Contains(query, "SELECT unit_id, state") {
		return queryPostgreSQLLeaseStates(args, data), true
	}
	return nil, false
}

func queryPostgreSQLLeaseIDs(args []driver.NamedValue, data *postgresqlOwnerTestData) driver.Rows {
	prefix := fmt.Sprintf("%s\x00", fmt.Sprint(args[2].Value))
	var rows [][]driver.Value
	for key := range data.leases {
		if suffix, ok := strings.CutPrefix(key, prefix); ok {
			rows = append(rows, []driver.Value{suffix})
		}
	}
	sort.Slice(rows, func(left, right int) bool { return fmt.Sprint(rows[left][0]) < fmt.Sprint(rows[right][0]) })
	return newPostgreSQLOwnerTestRows([]string{"unit_id"}, rows)
}

func queryPostgreSQLLeaseStates(args []driver.NamedValue, data *postgresqlOwnerTestData) driver.Rows {
	prefix := fmt.Sprintf("%s\x00", fmt.Sprint(args[2].Value))
	var rows [][]driver.Value
	for key, lease := range data.leases {
		if suffix, ok := strings.CutPrefix(key, prefix); ok {
			rows = append(rows, []driver.Value{suffix, lease[0]})
		}
	}
	return newPostgreSQLOwnerTestRows([]string{"unit_id", "state"}, rows)
}

func queryPostgreSQLReservation(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (driver.Rows, bool) {
	switch {
	case strings.Contains(query, "SELECT node_id, probe_identity, probe_boot_id, cpu_milli, memory_bytes, max_concurrency, state, last_revision"):
		return queryPostgreSQLReservationProjection(args, data), true
	case strings.Contains(query, "SELECT count(*)"):
		return queryPostgreSQLReservationCount(args, data), true
	case strings.Contains(query, "FROM heteroci_owner.routing_reservations") && strings.Contains(query, "SELECT state"):
		return queryPostgreSQLReservationStates(args, data), true
	default:
		return nil, false
	}
}
func queryPostgreSQLReservationProjection(args []driver.NamedValue, data *postgresqlOwnerTestData) driver.Rows {
	var rows [][]driver.Value
	for _, reservation := range data.reservations {
		if reservationMatchesScope(reservation, args) {
			rows = append(rows, []driver.Value{reservation[3], reservation[4], reservation[5], reservation[6], reservation[7], reservation[8], reservation[9], reservation[10]})
		}
	}
	sort.Slice(rows, func(left, right int) bool { return fmt.Sprint(rows[left][0]) < fmt.Sprint(rows[right][0]) })
	return newPostgreSQLOwnerTestRows([]string{"node_id", "probe_identity", "probe_boot_id", "cpu_milli", "memory_bytes", "max_concurrency", "state", "last_revision"}, rows)
}
func queryPostgreSQLReservationCount(args []driver.NamedValue, data *postgresqlOwnerTestData) driver.Rows {
	count := int64(0)
	for _, reservation := range data.reservations {
		if reservationMatchesScope(reservation, args) && fmt.Sprint(reservation[9]) != "finished" {
			count++
		}
	}
	return newPostgreSQLOwnerTestRows([]string{"count"}, [][]driver.Value{{count}})
}
func queryPostgreSQLReservationStates(args []driver.NamedValue, data *postgresqlOwnerTestData) driver.Rows {
	var rows [][]driver.Value
	for _, reservation := range data.reservations {
		if reservationMatchesScope(reservation, args) {
			rows = append(rows, []driver.Value{reservation[9]})
		}
	}
	return newPostgreSQLOwnerTestRows([]string{"state"}, rows)
}
func reservationMatchesScope(reservation []driver.Value, args []driver.NamedValue) bool {
	return fmt.Sprint(reservation[0]) == fmt.Sprint(args[0].Value) && fmt.Sprint(reservation[1]) == fmt.Sprint(args[1].Value) && fmt.Sprint(reservation[2]) == fmt.Sprint(args[2].Value)
}

func namedInt64(value driver.Value) int64 {
	if parsed, ok := value.(int64); ok {
		return parsed
	}
	return 0
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
	if strings.Contains(query, "pg_is_in_recovery") {
		return queryPostgreSQLServerMetadata(conn), true
	}
	if strings.Contains(query, "string_agg") {
		return queryPostgreSQLSchemaMetadata(conn), true
	}
	return nil, false
}

func queryPostgreSQLServerMetadata(conn *postgresqlOwnerTestConn) driver.Rows {
	return newPostgreSQLOwnerTestRows([]string{"in_recovery", "transaction_read_only", "synchronous_commit", "server_address", "current_user"}, [][]driver.Value{{conn.state.inRecovery, conn.state.readOnly, conn.state.synchronousCommit, conn.state.serverAddress, conn.state.currentUser}})
}

func queryPostgreSQLSchemaMetadata(conn *postgresqlOwnerTestConn) driver.Rows {
	if conn.state.schemaOwner == "" {
		return newPostgreSQLOwnerTestRows([]string{"schema_owner", "schema_version", "table_ownership", "index_names"}, nil)
	}
	parts := make([]string, 0, len(conn.state.tableOwners))
	for _, name := range postgresqlOwnerTableNames() {
		if owner, ok := conn.state.tableOwners[name]; ok {
			parts = append(parts, name+":"+owner)
		}
	}
	indexes := make([]string, 0, len(conn.state.indexNames))
	for _, name := range postgresqlOwnerIndexNames() {
		if _, ok := conn.state.indexNames[name]; ok {
			indexes = append(indexes, name)
		}
	}
	return newPostgreSQLOwnerTestRows([]string{"schema_owner", "schema_version", "table_ownership", "index_names"}, [][]driver.Value{{conn.state.schemaOwner, conn.state.schemaVersion, strings.Join(parts, ","), strings.Join(indexes, ",")}})
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
	if err := conn.execPostgreSQLOwnerStatement(query, args, data); err != nil {
		return nil, err
	}
	if conn.state.zeroReservationReleaseUpdate && strings.HasPrefix(query, "UPDATE heteroci_owner.routing_reservations SET state = 'finished'") {
		return driver.RowsAffected(0), nil
	}
	if strings.HasPrefix(query, "UPDATE heteroci_owner.routing_reservations SET state = 'started'") {
		return driver.RowsAffected(countPostgreSQLReservationRows(data, args[0].Value, "started")), nil
	}
	if strings.HasPrefix(query, "UPDATE heteroci_owner.routing_reservations SET state = 'finished'") {
		return driver.RowsAffected(countPostgreSQLReservationRows(data, args[0].Value, "finished")), nil
	}
	return driver.RowsAffected(1), nil
}

func countPostgreSQLReservationRows(data *postgresqlOwnerTestData, revision driver.Value, state string) int64 {
	var count int64
	for _, reservation := range data.reservations {
		if fmt.Sprint(reservation[9]) == state && fmt.Sprint(reservation[10]) == fmt.Sprint(revision) {
			count++
		}
	}
	return count
}

func (conn *postgresqlOwnerTestConn) execPostgreSQLOwnerStatement(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) error {
	if handled, err := conn.execPostgreSQLLifecycleMutation(query, args, data); handled {
		return err
	}
	switch {
	case strings.HasPrefix(query, "UPDATE heteroci_owner.owner_heads SET revision"):
		data.head[2], data.head[3], data.head[4], data.head[5] = args[0].Value, args[1].Value, args[2].Value, args[3].Value
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.operation_journal"):
		if data.operations == nil {
			data.operations = make(map[string][]driver.Value)
		}
		values := namedValues(args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10], args[11], args[12], args[13])
		if len(args) >= 17 {
			values = append(values, args[14].Value, args[15].Value, args[16].Value)
		} else {
			values = append(values, nil, nil, nil)
		}
		data.operations[fmt.Sprint(args[1].Value)] = values
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.owner_epochs"), strings.HasPrefix(query, "INSERT INTO heteroci_owner.routing_"), strings.HasPrefix(query, "UPDATE heteroci_owner.owner_epochs"):
	default:
		return fmt.Errorf("unexpected PostgreSQL owner exec: %s", query)
	}
	return nil
}

func (conn *postgresqlOwnerTestConn) execPostgreSQLLifecycleMutation(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (bool, error) {
	if handled, err := execPostgreSQLLifecycleInsert(query, args, data); handled {
		return true, err
	}
	return execPostgreSQLLifecycleUpdate(query, args, data)
}

func execPostgreSQLLifecycleInsert(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (bool, error) {
	switch {
	case strings.HasPrefix(query, "SET LOCAL synchronous_commit"):
		return true, nil
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.owner_epochs"):
		if data.epochs == nil {
			data.epochs = make(map[int64][]driver.Value)
		}
		data.epochs[namedInt64(args[1].Value)] = []driver.Value{args[2].Value, args[3].Value}
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.routing_runs"):
		if data.runs == nil {
			data.runs = make(map[string][]driver.Value)
		}
		data.runs[fmt.Sprint(args[2].Value)] = namedValues(args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8])
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.routing_reservations"):
		if data.reservations == nil {
			data.reservations = make(map[string][]driver.Value)
		}
		key := fmt.Sprintf("%s\x00%s", fmt.Sprint(args[2].Value), fmt.Sprint(args[3].Value))
		data.reservations[key] = namedValues(args[0], args[1], args[2], args[3], args[4], args[5], args[6], args[7], args[8], args[9], args[10])
	case strings.HasPrefix(query, "INSERT INTO heteroci_owner.routing_leases"):
		if data.leases == nil {
			data.leases = make(map[string][]driver.Value)
		}
		key := fmt.Sprintf("%s\x00%s", fmt.Sprint(args[2].Value), fmt.Sprint(args[3].Value))
		data.leases[key] = []driver.Value{args[4].Value, args[5].Value, nil, nil, args[6].Value, nil, nil}
	default:
		return false, nil
	}
	return true, nil
}

func execPostgreSQLLifecycleUpdate(query string, args []driver.NamedValue, data *postgresqlOwnerTestData) (bool, error) {
	switch {
	case strings.HasPrefix(query, "UPDATE heteroci_owner.routing_leases SET state = $1, started_bytes"):
		return true, updatePostgreSQLStartedFixture(data, args)
	case strings.HasPrefix(query, "UPDATE heteroci_owner.routing_leases SET state = 'finished'"):
		return true, updatePostgreSQLTerminalFixture(data, args)
	case strings.HasPrefix(query, "UPDATE heteroci_owner.routing_runs SET last_revision"):
		return true, updatePostgreSQLRunFixture(data, args)
	case strings.HasPrefix(query, "UPDATE heteroci_owner.routing_reservations SET state"):
		return true, updatePostgreSQLReservationFixtures(data, args, query)
	default:
		return false, nil
	}
}

func updatePostgreSQLStartedFixture(data *postgresqlOwnerTestData, args []driver.NamedValue) error {
	key := fmt.Sprintf("%s\x00%s", fmt.Sprint(args[6].Value), fmt.Sprint(args[7].Value))
	lease, ok := data.leases[key]
	if !ok || fmt.Sprint(lease[0]) != fmt.Sprint(args[8].Value) || fmt.Sprint(lease[4]) != fmt.Sprint(args[9].Value) {
		return fmt.Errorf("fake PostgreSQL started lease CAS mismatch")
	}
	lease[0], lease[2], lease[4], lease[5] = args[0].Value, args[1].Value, args[2].Value, args[3].Value
	return nil
}

func updatePostgreSQLTerminalFixture(data *postgresqlOwnerTestData, args []driver.NamedValue) error {
	key := fmt.Sprintf("%s\x00%s", fmt.Sprint(args[5].Value), fmt.Sprint(args[6].Value))
	lease, ok := data.leases[key]
	if !ok || fmt.Sprint(lease[0]) != fmt.Sprint(args[7].Value) || fmt.Sprint(lease[4]) != fmt.Sprint(args[8].Value) {
		return fmt.Errorf("fake PostgreSQL terminal lease CAS mismatch")
	}
	lease[0], lease[3], lease[4], lease[6] = "finished", args[0].Value, args[1].Value, args[2].Value
	return nil
}

func updatePostgreSQLRunFixture(data *postgresqlOwnerTestData, args []driver.NamedValue) error {
	key := fmt.Sprint(args[3].Value)
	run, ok := data.runs[key]
	if !ok || fmt.Sprint(run[6]) != fmt.Sprint(args[4].Value) {
		return fmt.Errorf("fake PostgreSQL run CAS mismatch")
	}
	run[6] = args[0].Value
	return nil
}

func updatePostgreSQLReservationFixtures(data *postgresqlOwnerTestData, args []driver.NamedValue, query string) error {
	for _, reservation := range data.reservations {
		if fmt.Sprint(reservation[2]) != fmt.Sprint(args[3].Value) {
			continue
		}
		if strings.Contains(query, "state = 'started'") && fmt.Sprint(reservation[9]) == "planned" {
			reservation[9], reservation[10] = "started", args[0].Value
		}
		if strings.Contains(query, "state = 'finished'") && fmt.Sprint(reservation[9]) != "finished" {
			reservation[9], reservation[10] = "finished", args[0].Value
		}
	}
	return nil
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
	err := conn.state.rollbackError
	conn.state.rollbackError = nil
	return err
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
	state := &postgresqlOwnerTestState{readOnly: "off", synchronousCommit: PostgreSQLOwnerSynchronousCommitRemoteApply, serverAddress: "203.0.113.10", currentUser: PostgreSQLOwnerRole, schemaOwner: PostgreSQLOwnerRole, schemaVersion: PostgreSQLOwnerRuntimeSchemaVersion, tableOwners: make(map[string]string), indexNames: make(map[string]struct{})}
	for _, name := range postgresqlOwnerTableNames() {
		state.tableOwners[name] = PostgreSQLOwnerRole
	}
	for _, name := range postgresqlOwnerIndexNames() {
		state.indexNames[name] = struct{}{}
	}
	name := "heteroci-postgresql-owner-test-" + strings.ReplaceAll(t.Name(), "/", "-") + fmt.Sprintf("-%d", time.Now().UnixNano())
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
