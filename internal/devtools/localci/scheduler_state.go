package localci

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schedulerSchemaVersion = 1

var (
	errRecoveryUnproved  = errors.New("running workload could not be proved alive during recovery")
	errHandshakeConsumed = errors.New("opaque handshake token was already consumed")
	errHandshakeExpired  = errors.New("opaque handshake token expired")
)

type schedulerState struct {
	db        *sql.DB
	daemonKey string
	now       func() time.Time
}

type queueState interface {
	saveKernel(context.Context, *schedulerKernel) error
	loadKernel(context.Context, daemonIdentity) (*schedulerKernel, error)
}

type leaseState interface {
	reconcileLeases(context.Context, []observedLease) error
}

type outboxState interface {
	transitionWithEvent(context.Context, workloadID, outboxEvent) (outboxEvent, error)
	replayOutbox(context.Context, string, invocationID) ([]outboxEvent, error)
	ackOutbox(context.Context, string, invocationID, uint64) error
}

type handshakeTokenState interface {
	issueOpaqueHandshake(context.Context, opaqueHandshakeBinding, time.Duration) (string, error)
	consumeOpaqueHandshake(context.Context, string, opaqueHandshakeBinding) error
}

var (
	_ queueState          = (*schedulerState)(nil)
	_ leaseState          = (*schedulerState)(nil)
	_ outboxState         = (*schedulerState)(nil)
	_ handshakeTokenState = (*schedulerState)(nil)
)

type observedLease struct {
	id         string
	workloadID workloadID
}

type outboxEvent struct {
	subscriberID string
	invocationID invocationID
	sequence     uint64
	status       workloadState
	payload      []byte
}

type opaqueHandshakeBinding struct {
	jobID        workloadID
	invocationID invocationID
	containerID  string
}

// opaqueHandshakeBinding is owner-local scheduler state, not the final signed cross-boundary SignedJobToken.

// openSchedulerState 打开 daemon 私有 SQLite，并严格校验 schema 与 daemon key。
func openSchedulerState(
	ctx context.Context,
	statePath string,
	identity daemonIdentity,
) (*schedulerState, error) {
	if strings.TrimSpace(statePath) == "" {
		return nil, errors.New("scheduler state path is required")
	}
	if strings.TrimSpace(identity.key) == "" {
		return nil, errors.New("validated daemon identity is required")
	}
	stateFile, err := openCurrentUIDPrivateFile(statePath, identity.ownerUID)
	if err != nil {
		return nil, fmt.Errorf("validate scheduler state file: %w", err)
	}
	if err := stateFile.Close(); err != nil {
		return nil, fmt.Errorf("close scheduler state validation handle: %w", err)
	}
	db, err := sql.Open("sqlite", statePath)
	if err != nil {
		return nil, fmt.Errorf("open scheduler state: %w", err)
	}
	db.SetMaxOpenConns(1)
	state := &schedulerState{db: db, daemonKey: identity.key, now: time.Now}
	if err := state.prepare(ctx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close invalid scheduler state: %w", closeErr))
		}
		return nil, err
	}
	return state, nil
}

// prepare 配置 SQLite 并区分空库初始化与既有库严格校验。
func (s *schedulerState) prepare(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable scheduler foreign keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set scheduler busy timeout: %w", err)
	}
	empty, err := schedulerDatabaseEmpty(ctx, s.db)
	if err != nil {
		return err
	}
	if empty {
		if err := createSchedulerSchema(ctx, s.db, s.daemonKey); err != nil {
			return err
		}
	}
	return validateSchedulerSchema(ctx, s.db, s.daemonKey)
}

func schedulerDatabaseEmpty(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'",
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("inspect scheduler schema: %w", err)
	}
	return count == 0, nil
}

func createSchedulerSchema(ctx context.Context, db *sql.DB, daemonKey string) (retErr error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scheduler schema creation: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "scheduler schema creation")
	if _, err := tx.ExecContext(ctx, schedulerSchemaSQL); err != nil {
		return fmt.Errorf("create scheduler schema: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO scheduler_schema (id, version, daemon_key) VALUES (1, ?, ?)",
		schedulerSchemaVersion,
		daemonKey,
	); err != nil {
		return fmt.Errorf("record scheduler schema identity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduler schema creation: %w", err)
	}
	return nil
}

// validateSchedulerSchema 拒绝版本、daemon key 或 SQLite 完整性漂移。
func validateSchedulerSchema(ctx context.Context, db *sql.DB, daemonKey string) error {
	var version int
	var storedDaemonKey string
	if err := db.QueryRowContext(
		ctx,
		"SELECT version, daemon_key FROM scheduler_schema WHERE id = 1",
	).Scan(&version, &storedDaemonKey); err != nil {
		return fmt.Errorf("read scheduler schema identity: %w", err)
	}
	if version != schedulerSchemaVersion {
		return fmt.Errorf("scheduler schema version %d does not match required %d", version, schedulerSchemaVersion)
	}
	if storedDaemonKey != daemonKey {
		return errors.New("scheduler state daemon identity mismatch")
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("check scheduler state integrity: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("scheduler state integrity check failed: %s", integrity)
	}
	return nil
}

const schedulerSchemaSQL = `
CREATE TABLE scheduler_schema (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	version INTEGER NOT NULL,
	daemon_key TEXT NOT NULL
);
CREATE TABLE scheduler_workloads (
	id TEXT PRIMARY KEY,
	invocation_id TEXT NOT NULL,
	enqueue_seq INTEGER NOT NULL CHECK (enqueue_seq > 0),
	sub_seq INTEGER NOT NULL CHECK (sub_seq >= 0),
	kind INTEGER NOT NULL,
	service_count INTEGER NOT NULL CHECK (service_count >= 0),
	status TEXT NOT NULL,
	gang_bypasses INTEGER NOT NULL CHECK (gang_bypasses >= 0)
);
CREATE TABLE scheduler_dependencies (
	workload_id TEXT NOT NULL REFERENCES scheduler_workloads(id) ON DELETE CASCADE,
	dependency_id TEXT NOT NULL REFERENCES scheduler_workloads(id),
	PRIMARY KEY (workload_id, dependency_id)
);
CREATE TABLE scheduler_leases (
	id TEXT PRIMARY KEY,
	workload_id TEXT NOT NULL REFERENCES scheduler_workloads(id) ON DELETE CASCADE,
	kind INTEGER NOT NULL
);
CREATE TABLE scheduler_outbox (
	subscriber_id TEXT NOT NULL,
	invocation_id TEXT NOT NULL,
	event_seq INTEGER NOT NULL CHECK (event_seq > 0),
	status TEXT NOT NULL,
	payload BLOB NOT NULL,
	PRIMARY KEY (subscriber_id, invocation_id, event_seq)
);
CREATE TABLE scheduler_outbox_cursors (
	subscriber_id TEXT NOT NULL,
	invocation_id TEXT NOT NULL,
	ack_seq INTEGER NOT NULL CHECK (ack_seq >= 0),
	PRIMARY KEY (subscriber_id, invocation_id)
);
CREATE TABLE scheduler_handshake_tokens (
	token_hash TEXT PRIMARY KEY,
	job_id TEXT NOT NULL,
	invocation_id TEXT NOT NULL,
	container_id TEXT NOT NULL,
	expires_at INTEGER NOT NULL,
	consumed_at INTEGER
);
`

// saveKernel 将 queue、DAG 与 lease 作为一个 daemon 快照原子持久化。
func (s *schedulerState) saveKernel(ctx context.Context, kernel *schedulerKernel) (retErr error) {
	if kernel == nil || kernel.identity.key != s.daemonKey {
		return errors.New("scheduler kernel daemon identity mismatch")
	}
	if err := kernel.validateDAG(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scheduler snapshot: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "scheduler snapshot")
	if err := clearSchedulerSnapshot(ctx, tx); err != nil {
		return err
	}
	if err := insertSchedulerNodes(ctx, tx, kernel); err != nil {
		return err
	}
	if err := insertSchedulerDependencies(ctx, tx, kernel); err != nil {
		return err
	}
	if err := insertSchedulerLeases(ctx, tx, kernel); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduler snapshot: %w", err)
	}
	return nil
}

func clearSchedulerSnapshot(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"scheduler_dependencies", "scheduler_leases", "scheduler_workloads"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	return nil
}

func insertSchedulerNodes(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	const query = `INSERT INTO scheduler_workloads
		(id, invocation_id, enqueue_seq, sub_seq, kind, service_count, status, gang_bypasses)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	for _, node := range kernel.nodes {
		if _, err := tx.ExecContext(
			ctx,
			query,
			node.spec.id,
			node.spec.invocationID,
			node.spec.enqueueSeq,
			node.spec.subSeq,
			node.spec.kind,
			node.spec.serviceCount,
			node.state,
			node.gangBypasses,
		); err != nil {
			return fmt.Errorf("persist workload %q: %w", node.spec.id, err)
		}
	}
	return nil
}

// insertSchedulerDependencies 在 workload 全部存在后写入 DAG 边。
func insertSchedulerDependencies(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	for _, node := range kernel.nodes {
		for _, dependencyID := range node.spec.dependencies {
			if _, err := tx.ExecContext(
				ctx,
				"INSERT INTO scheduler_dependencies (workload_id, dependency_id) VALUES (?, ?)",
				node.spec.id,
				dependencyID,
			); err != nil {
				return fmt.Errorf("persist dependency %q -> %q: %w", node.spec.id, dependencyID, err)
			}
		}
	}
	return nil
}

func insertSchedulerLeases(ctx context.Context, tx *sql.Tx, kernel *schedulerKernel) error {
	for _, lease := range kernel.leases {
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO scheduler_leases (id, workload_id, kind) VALUES (?, ?, ?)",
			lease.id,
			lease.workloadID,
			lease.kind,
		); err != nil {
			return fmt.Errorf("persist lease %q: %w", lease.id, err)
		}
	}
	return nil
}

// loadKernel 恢复 queue、DAG 与 lease，并在返回前验证全部容量不变量。
func (s *schedulerState) loadKernel(
	ctx context.Context,
	identity daemonIdentity,
) (*schedulerKernel, error) {
	if identity.key != s.daemonKey {
		return nil, errors.New("scheduler kernel daemon identity mismatch")
	}
	kernel, err := newSchedulerKernel(identity)
	if err != nil {
		return nil, err
	}
	if err := s.loadNodes(ctx, kernel); err != nil {
		return nil, err
	}
	if err := s.loadDependencies(ctx, kernel); err != nil {
		return nil, err
	}
	if err := s.loadLeases(ctx, kernel); err != nil {
		return nil, err
	}
	if err := validateRecoveredKernel(kernel); err != nil {
		return nil, err
	}
	return kernel, nil
}

// loadNodes 恢复 workload 基础字段，依赖与 lease 在后续阶段装载。
func (s *schedulerState) loadNodes(ctx context.Context, kernel *schedulerKernel) (retErr error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, invocation_id, enqueue_seq, sub_seq, kind, service_count, status, gang_bypasses
		FROM scheduler_workloads`)
	if err != nil {
		return fmt.Errorf("query scheduler workloads: %w", err)
	}
	defer closeRows(rows, &retErr, "scheduler workload rows")
	for rows.Next() {
		var spec workloadSpec
		var state workloadState
		var bypasses int
		if err := rows.Scan(
			&spec.id,
			&spec.invocationID,
			&spec.enqueueSeq,
			&spec.subSeq,
			&spec.kind,
			&spec.serviceCount,
			&state,
			&bypasses,
		); err != nil {
			return fmt.Errorf("scan scheduler workload: %w", err)
		}
		if err := kernel.enqueue(spec); err != nil {
			return fmt.Errorf("restore workload %q: %w", spec.id, err)
		}
		kernel.nodes[spec.id].state = state
		kernel.nodes[spec.id].gangBypasses = bypasses
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scheduler workloads: %w", err)
	}
	return nil
}

// loadDependencies 恢复 DAG 边并拒绝悬空 workload 引用。
func (s *schedulerState) loadDependencies(ctx context.Context, kernel *schedulerKernel) (retErr error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT workload_id, dependency_id FROM scheduler_dependencies",
	)
	if err != nil {
		return fmt.Errorf("query scheduler dependencies: %w", err)
	}
	defer closeRows(rows, &retErr, "scheduler dependency rows")
	for rows.Next() {
		var ownerID workloadID
		var dependencyID workloadID
		if err := rows.Scan(&ownerID, &dependencyID); err != nil {
			return fmt.Errorf("scan scheduler dependency: %w", err)
		}
		node, exists := kernel.nodes[ownerID]
		if !exists {
			return fmt.Errorf("dependency references unknown workload %q", ownerID)
		}
		node.spec.dependencies = append(node.spec.dependencies, dependencyID)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scheduler dependencies: %w", err)
	}
	return nil
}

// loadLeases 恢复统一容量 lease 并拒绝未知 owner 与重复 ID。
func (s *schedulerState) loadLeases(ctx context.Context, kernel *schedulerKernel) (retErr error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, workload_id, kind FROM scheduler_leases")
	if err != nil {
		return fmt.Errorf("query scheduler leases: %w", err)
	}
	defer closeRows(rows, &retErr, "scheduler lease rows")
	for rows.Next() {
		var lease slotLease
		if err := rows.Scan(&lease.id, &lease.workloadID, &lease.kind); err != nil {
			return fmt.Errorf("scan scheduler lease: %w", err)
		}
		if _, exists := kernel.nodes[lease.workloadID]; !exists {
			return fmt.Errorf("lease %q references unknown workload %q", lease.id, lease.workloadID)
		}
		if _, duplicate := kernel.leases[lease.id]; duplicate {
			return fmt.Errorf("duplicate restored lease %q", lease.id)
		}
		kernel.leases[lease.id] = lease
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate scheduler leases: %w", err)
	}
	return nil
}

// validateRecoveredKernel 在恢复后统一检查 DAG、容量、状态和 build 上限。
func validateRecoveredKernel(kernel *schedulerKernel) error {
	if err := kernel.validateDAG(); err != nil {
		return err
	}
	if len(kernel.leases) > maxActiveWorkloads {
		return fmt.Errorf("restored lease count %d exceeds capacity", len(kernel.leases))
	}
	leaseCounts := make(map[workloadID]int, len(kernel.nodes))
	for _, lease := range kernel.leases {
		if !lease.kind.valid() {
			return fmt.Errorf("restored lease %q has invalid kind %d", lease.id, lease.kind)
		}
		leaseCounts[lease.workloadID]++
	}
	for id, node := range kernel.nodes {
		if err := validateRecoveredNode(id, node, leaseCounts[id]); err != nil {
			return err
		}
	}
	if kernel.activeBuilds() > maxImageBuilds {
		return fmt.Errorf("restored active builds exceed maximum %d", maxImageBuilds)
	}
	return nil
}

func validateRecoveredNode(id workloadID, node *workloadNode, leaseCount int) error {
	expected := 0
	if node.state == stateStarted {
		expected = 1 + node.spec.serviceCount
	}
	if !validWorkloadState(node.state) {
		return fmt.Errorf("workload %q has invalid restored state %q", id, node.state)
	}
	if leaseCount != expected {
		return fmt.Errorf("workload %q restored lease count %d does not match %d", id, leaseCount, expected)
	}
	return nil
}

func validWorkloadState(state workloadState) bool {
	return state == stateQueued || state == stateStarted || state.terminal()
}

// reconcileLeases 先拒绝未知或重复观察，再原子隔离无法证明的 running workload。
func (s *schedulerState) reconcileLeases(ctx context.Context, observed []observedLease) error {
	persisted, err := s.persistedLeases(ctx)
	if err != nil {
		return err
	}
	observedIDs, err := validateObservedLeases(observed, persisted)
	if err != nil {
		return err
	}
	missingWorkloads := make(map[workloadID]struct{})
	for id, lease := range persisted {
		if _, exists := observedIDs[id]; !exists {
			missingWorkloads[lease.workloadID] = struct{}{}
		}
	}
	if len(missingWorkloads) == 0 {
		return nil
	}
	if err := s.markRecoveryFailed(ctx, missingWorkloads); err != nil {
		return err
	}
	return fmt.Errorf("%w: %d workload(s)", errRecoveryUnproved, len(missingWorkloads))
}

func (s *schedulerState) persistedLeases(ctx context.Context) (result map[string]observedLease, retErr error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, workload_id FROM scheduler_leases")
	if err != nil {
		return nil, fmt.Errorf("query persisted leases: %w", err)
	}
	defer closeRows(rows, &retErr, "persisted lease rows")
	result = make(map[string]observedLease)
	for rows.Next() {
		var lease observedLease
		if err := rows.Scan(&lease.id, &lease.workloadID); err != nil {
			return nil, fmt.Errorf("scan persisted lease: %w", err)
		}
		result[lease.id] = lease
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate persisted leases: %w", err)
	}
	return result, nil
}

func validateObservedLeases(
	observed []observedLease,
	persisted map[string]observedLease,
) (map[string]struct{}, error) {
	observedIDs := make(map[string]struct{}, len(observed))
	for _, lease := range observed {
		if _, duplicate := observedIDs[lease.id]; duplicate {
			return nil, fmt.Errorf("duplicate observed lease %q", lease.id)
		}
		expected, exists := persisted[lease.id]
		if !exists {
			return nil, fmt.Errorf("unknown observed lease %q", lease.id)
		}
		if expected.workloadID != lease.workloadID {
			return nil, fmt.Errorf("observed lease %q owner mismatch", lease.id)
		}
		observedIDs[lease.id] = struct{}{}
	}
	return observedIDs, nil
}

// markRecoveryFailed 原子标记无法证明存活的 workload 并删除其 lease。
func (s *schedulerState) markRecoveryFailed(
	ctx context.Context,
	workloadIDs map[workloadID]struct{},
) (retErr error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recovery failure transition: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "recovery failure transition")
	for workloadID := range workloadIDs {
		result, err := tx.ExecContext(
			ctx,
			"UPDATE scheduler_workloads SET status = ? WHERE id = ? AND status = ?",
			stateInfraFailed,
			workloadID,
			stateStarted,
		)
		if err != nil {
			return fmt.Errorf("mark workload %q infra failed: %w", workloadID, err)
		}
		if err := requireOneRow(result, "mark recovered workload infra failed"); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			"DELETE FROM scheduler_leases WHERE workload_id = ?",
			workloadID,
		); err != nil {
			return fmt.Errorf("delete unproved workload leases: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit recovery failure transition: %w", err)
	}
	return nil
}

// transitionWithEvent 在同一事务中提交 workload 状态和 subscriber outbox。
func (s *schedulerState) transitionWithEvent(
	ctx context.Context,
	id workloadID,
	event outboxEvent,
) (stored outboxEvent, retErr error) {
	if err := validateOutboxEvent(event); err != nil {
		return outboxEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return outboxEvent{}, fmt.Errorf("begin outbox transition: %w", err)
	}
	defer rollbackTransaction(tx, &retErr, "outbox transition")
	current, err := loadAllowedOutboxTransition(ctx, tx, id, event)
	if err != nil {
		return outboxEvent{}, err
	}
	result, err := tx.ExecContext(
		ctx,
		`UPDATE scheduler_workloads SET status = ?
		WHERE id = ? AND invocation_id = ? AND status = ?`,
		event.status,
		id,
		event.invocationID,
		current,
	)
	if err != nil {
		return outboxEvent{}, fmt.Errorf("transition workload for outbox: %w", err)
	}
	if err := requireOneRow(result, "transition workload for outbox"); err != nil {
		return outboxEvent{}, err
	}
	if err := assignNextOutboxSequence(ctx, tx, &event); err != nil {
		return outboxEvent{}, err
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO scheduler_outbox
		(subscriber_id, invocation_id, event_seq, status, payload) VALUES (?, ?, ?, ?, ?)`,
		event.subscriberID,
		event.invocationID,
		event.sequence,
		event.status,
		event.payload,
	); err != nil {
		return outboxEvent{}, fmt.Errorf("insert scheduler outbox event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return outboxEvent{}, fmt.Errorf("commit outbox transition: %w", err)
	}
	event.payload = append([]byte(nil), event.payload...)
	return event, nil
}

// validateOutboxEvent 接受 queued、started 和终态事件，并拒绝调用方伪造 sequence。
func validateOutboxEvent(event outboxEvent) error {
	if strings.TrimSpace(event.subscriberID) == "" || strings.TrimSpace(string(event.invocationID)) == "" {
		return errors.New("outbox subscriber and invocation are required")
	}
	if !validWorkloadState(event.status) {
		return fmt.Errorf("outbox status %q is invalid", event.status)
	}
	if len(event.payload) == 0 {
		return errors.New("outbox payload is required")
	}
	if event.sequence != 0 {
		return errors.New("outbox sequence is assigned by scheduler state")
	}
	return nil
}

// loadAllowedOutboxTransition 在事务内读取 workload owner/现态并校验状态机边。
func loadAllowedOutboxTransition(
	ctx context.Context,
	tx *sql.Tx,
	id workloadID,
	event outboxEvent,
) (workloadState, error) {
	var owner invocationID
	var current workloadState
	err := tx.QueryRowContext(
		ctx,
		"SELECT invocation_id, status FROM scheduler_workloads WHERE id = ?",
		id,
	).Scan(&owner, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("unknown workload %q", id)
	}
	if err != nil {
		return "", fmt.Errorf("read workload transition owner: %w", err)
	}
	if owner != event.invocationID {
		return "", fmt.Errorf("workload %q invocation owner mismatch", id)
	}
	if !allowedOutboxTransition(current, event.status) {
		return "", fmt.Errorf("workload %q transition %q -> %q is not allowed", id, current, event.status)
	}
	return current, nil
}

func allowedOutboxTransition(current, next workloadState) bool {
	if current == stateQueued {
		return next == stateQueued || next == stateStarted
	}
	return current == stateStarted && next.terminal()
}

func assignNextOutboxSequence(ctx context.Context, tx *sql.Tx, event *outboxEvent) error {
	var current uint64
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(event_seq), 0) FROM scheduler_outbox
		WHERE subscriber_id = ? AND invocation_id = ?`,
		event.subscriberID,
		event.invocationID,
	).Scan(&current); err != nil {
		return fmt.Errorf("read scheduler outbox sequence: %w", err)
	}
	event.sequence = current + 1
	return nil
}

// replayOutbox 返回 durable ack cursor 之后的严格有序事件。
func (s *schedulerState) replayOutbox(
	ctx context.Context,
	subscriberID string,
	invocationID invocationID,
) (events []outboxEvent, retErr error) {
	if strings.TrimSpace(subscriberID) == "" || strings.TrimSpace(string(invocationID)) == "" {
		return nil, errors.New("outbox subscriber and invocation are required")
	}
	cursor, err := s.outboxCursor(ctx, subscriberID, invocationID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT event_seq, status, payload FROM scheduler_outbox
		WHERE subscriber_id = ? AND invocation_id = ? AND event_seq > ?
		ORDER BY event_seq ASC`,
		subscriberID,
		invocationID,
		cursor,
	)
	if err != nil {
		return nil, fmt.Errorf("query scheduler outbox replay: %w", err)
	}
	defer closeRows(rows, &retErr, "scheduler outbox rows")
	events = make([]outboxEvent, 0)
	for rows.Next() {
		event := outboxEvent{subscriberID: subscriberID, invocationID: invocationID}
		if err := rows.Scan(&event.sequence, &event.status, &event.payload); err != nil {
			return nil, fmt.Errorf("scan scheduler outbox event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduler outbox replay: %w", err)
	}
	return events, nil
}

func (s *schedulerState) outboxCursor(
	ctx context.Context,
	subscriberID string,
	invocationID invocationID,
) (uint64, error) {
	var cursor uint64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(ack_seq), 0) FROM scheduler_outbox_cursors
		WHERE subscriber_id = ? AND invocation_id = ?`,
		subscriberID,
		invocationID,
	).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("read scheduler outbox cursor: %w", err)
	}
	return cursor, nil
}

// ackOutbox 只允许 cursor 单调前进到已经持久化的 event sequence。
func (s *schedulerState) ackOutbox(
	ctx context.Context,
	subscriberID string,
	invocationID invocationID,
	sequence uint64,
) error {
	if sequence == 0 {
		return errors.New("outbox ack sequence must be positive")
	}
	var maximum uint64
	if err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(event_seq), 0) FROM scheduler_outbox
		WHERE subscriber_id = ? AND invocation_id = ?`,
		subscriberID,
		invocationID,
	).Scan(&maximum); err != nil {
		return fmt.Errorf("read scheduler outbox maximum: %w", err)
	}
	if sequence > maximum {
		return fmt.Errorf("outbox ack sequence %d exceeds emitted maximum %d", sequence, maximum)
	}
	cursor, err := s.outboxCursor(ctx, subscriberID, invocationID)
	if err != nil {
		return err
	}
	if sequence < cursor {
		return fmt.Errorf("outbox ack sequence %d regresses cursor %d", sequence, cursor)
	}
	_, err = s.db.ExecContext(
		ctx,
		`INSERT INTO scheduler_outbox_cursors (subscriber_id, invocation_id, ack_seq)
		VALUES (?, ?, ?)
		ON CONFLICT(subscriber_id, invocation_id) DO UPDATE SET ack_seq = excluded.ack_seq`,
		subscriberID,
		invocationID,
		sequence,
	)
	if err != nil {
		return fmt.Errorf("persist scheduler outbox cursor: %w", err)
	}
	return nil
}

func (s *schedulerState) close() error {
	if s == nil || s.db == nil {
		return errors.New("scheduler state is not open")
	}
	err := s.db.Close()
	s.db = nil
	return err
}
