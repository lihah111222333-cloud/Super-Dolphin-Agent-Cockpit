package db

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

var Module = fx.Module(
	"db",
	fx.Provide(NewDB),
	fx.Invoke(registerLifecycle),
)

// NewDB 打开产品运行时使用的 SQLite 数据库。
func NewDB(cfg *config.Config) (*sql.DB, error) {
	if cfg == nil {
		return nil, fmt.Errorf("SQLite DB config is nil")
	}
	return sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: cfg.SQLitePath})
}

// MinRequiredSchemaVersion 是当前二进制正常运行所需的 schema_migrations.version 下限。
// canonical command intent 在126中加入，append-only command journal在127中加入，orchestration identity owner在128中加入，canonical event store在129中加入，access grant与event wait owner在130中加入，send_message完整身份/授权profile在131中加入，CONTENT-1 metadata owner在132中加入，RESOURCE reserved-only skeleton在133中加入，B09 wakeup payload version floor在134中加入；
// 低于该版本的数据库不能通过启动门禁后再在第一次跨任务命令时延迟失败。
const MinRequiredSchemaVersion = 134

// requiredBaselineTables 返回当前最低 schema 启动所需的规范表集合。
func requiredBaselineTables() []string {
	return []string{
		// agent_codex_binding: 历史遗留表，数据合并至 agent_provider_binding.codex_thread_id，无活跃 sqlc query
		// topology_approval_archives: 无 sql/queries/*.sql 文件，不进入 SQLite runtime
		"agent_provider_binding",
		"agent_status",
		"agent_threads",
		"terminal_outcome_current_heads",
		"public_terminal_outcome_history",
		"terminal_outcome_private_dag_payloads",
		"terminal_outcome_outbox_v2",
		"audit_events",
		"bus_exception_logs",
		"prompts",
		"prompt_templates",
		"prompt_template_versions",
		"prompt_versions",
		"prompt_template_sections",
		"prompt_recall_topics",
		"prompt_routing_tests",
		"prompt_intent_drafts",
		"command_cards",
		"command_card_versions",
		"command_card_runs",
		"shared_files",
		"agent_feedback_events",
		"session_insights",
		"hook_pending_reviews",
		"agent_interactions",
		"topology_approvals",
		"ui_preferences",
		"system_logs",
		"task_traces",
		"cwd_instance_locks",
		"turn_dedupe_registry",
		"cron_jobs",
		"cron_job_runs",
		"task_acks",
		"task_dags",
		"task_dag_runs",
		"task_dag_nodes",
		"task_dag_wakeups",
		"task_dag_worker_leases",
		"workspace_run_files",
		"workspace_runs",
		"runtime_locks",
		"orchestration_direct_peer_grants",
		"orchestration_direct_peer_messages",
		"orchestration_direct_peer_events",
		"orchestration_direct_peer_event_outbox",
		"orchestration_command_intents",
		"orchestration_command_journal",
		"orchestration_identity_reservations",
		"orchestration_execution_incarnations",
		"orchestration_binding_preparations",
		"orchestration_current_bindings",
		"orchestration_event_source_states",
		"orchestration_event_ingress_receipts",
		"orchestration_agent_events",
		"orchestration_event_tombstones",
		"orchestration_event_materialization_cursors",
		"orchestration_access_grants",
		"orchestration_event_wait_handles",
		"orchestration_command_intent_profiles",
		"orchestration_legacy_command_content_quarantine",
		"orchestration_content_ingest_reservations",
		"orchestration_content_objects",
		"orchestration_content_private_payload_receipts",
		"orchestration_content_refs",
		"orchestration_command_content_deletion_fences",
		"orchestration_resource_effective_policy_snapshots",
		"orchestration_resource_reservations",
		"orchestration_resource_usage_ledger",
		"orchestration_legacy_wakeup_quarantine",
	}
}

type requiredSQLiteColumn struct {
	table  string
	column string
}

// requiredBaselineColumns 返回启动 schema gate 所需的关键列集合。
func requiredBaselineColumns() []requiredSQLiteColumn {
	required := []requiredSQLiteColumn{
		{table: "agent_threads", column: "prompt_snapshot"},
		{table: "agent_provider_binding", column: "provider_recovery_home"},
		{table: "hook_pending_reviews", column: "thread_id"},
		{table: "hook_pending_reviews", column: "turn_id"},
		{table: "hook_pending_reviews", column: "payload"},
		{table: "shared_files", column: "content_location"},
		{table: "turn_dedupe_registry", column: "terminal_at"},
		{table: "terminal_outcome_current_heads", column: "capability"},
		{table: "terminal_outcome_current_heads", column: "version"},
		{table: "terminal_outcome_current_heads", column: "terminal_identity"},
		{table: "public_terminal_outcome_history", column: "head_version"},
		{table: "public_terminal_outcome_history", column: "public_outcome_json"},
		{table: "terminal_outcome_private_dag_payloads", column: "payload_json"},
		{table: "terminal_outcome_outbox_v2", column: "public_payload_json"},
		{table: "terminal_outcome_outbox_v2", column: "claim_token"},
		{table: "orchestration_command_intents", column: "caller_principal_ref"},
		{table: "orchestration_command_intents", column: "scope_ref"},
		{table: "task_dag_wakeups", column: "payload_schema_version"},
		{table: "orchestration_legacy_wakeup_quarantine", column: "quarantine_id"},
		{table: "orchestration_legacy_wakeup_quarantine", column: "source_wakeup_id"},
		{table: "orchestration_legacy_wakeup_quarantine", column: "source_payload_digest"},
	}
	for _, column := range requiredCommandIntentProfileColumns() {
		required = append(required, requiredSQLiteColumn{table: "orchestration_command_intent_profiles", column: column})
	}
	for table, columns := range requiredContentSchemaColumns() {
		for _, column := range columns {
			required = append(required, requiredSQLiteColumn{table: table, column: column})
		}
	}
	for table, columns := range requiredResourceSchemaColumns() {
		for _, column := range columns {
			required = append(required, requiredSQLiteColumn{table: table, column: column})
		}
	}
	return required
}

func requiredCommandIntentProfileColumns() []string {
	return []string{
		"command_id", "profile_kind", "profile_version", "caller_profile", "caller_auth_context", "caller_auth_expires_at",
		"authorization_kind", "authorization_decision", "authorization_request_version", "authorization_request_id",
		"authorization_request_digest", "authorization_grant_id", "authorization_grant_revision", "authorization_capability",
		"authorization_requested_at", "authorization_decided_at", "authorization_expires_at", "grant_request_digest",
		"grant_issued_at", "grant_updated_at", "source_selector_version", "source_selector_kind", "source_agent_id",
		"source_identity_kind", "source_reservation_id", "source_reservation_generation", "source_incarnation_id",
		"source_incarnation_generation", "source_external_identity_ref", "source_binding_version", "source_binding_digest",
		"source_fence_ref", "source_active_process_receipt_ref", "source_binding_state", "source_binding_schema_version",
		"source_binding_updated_at", "target_selector_version", "target_selector_kind", "target_agent_id", "target_identity_kind",
		"target_reservation_id", "target_reservation_generation", "target_incarnation_id", "target_incarnation_generation",
		"target_external_identity_ref", "target_binding_version", "target_binding_digest", "target_fence_ref",
		"target_active_process_receipt_ref", "target_binding_state", "target_binding_schema_version", "target_binding_updated_at",
		"complete_intent_digest", "digest_domain", "digest_algorithm", "digest_key_id", "digest_key_version", "schema_version", "recorded_at",
	}
}

func requiredCommandIntentProfileTriggers() []string {
	return []string{
		"trg_orchestration_command_intent_profiles_exact_relation",
		"trg_orchestration_command_intent_profiles_immutable_update",
		"trg_orchestration_command_intent_profiles_immutable_delete",
	}
}

func requiredSQLiteTriggers() []string {
	triggers := append([]string(nil), requiredCommandIntentProfileTriggers()...)
	triggers = append(triggers, requiredContentSchemaTriggers()...)
	triggers = append(triggers, requiredResourceSchemaTriggers()...)
	return append(triggers,
		"trg_orchestration_legacy_wakeup_quarantine_no_update",
		"trg_orchestration_legacy_wakeup_quarantine_no_delete",
		"trg_task_dag_wakeups_v134_insert_version",
		"trg_task_dag_wakeups_v134_insert",
		"trg_task_dag_wakeups_v134_update",
		"trg_task_dag_wakeups_v134_version_immutable",
		"trg_task_dag_wakeups_v134_legacy_immutable",
	)
}

func requiredTerminalProtocolViews() []string {
	return []string{
		"terminal_outcome_heads",
		"public_terminal_outcomes",
		"terminal_outcome_outbox",
	}
}

// VerifyMinSchemaVersion 校验 SQLite schema 版本和基线表完整性。
func VerifyMinSchemaVersion(ctx context.Context, q any) error {
	return verifyMinSchemaVersion(ctx, q)
}

// verifyMinSchemaVersion 校验最低迁移版本以及启动所需的表、列和兼容视图。
func verifyMinSchemaVersion(ctx context.Context, q any) error {
	var maxVersion int
	if err := querySchemaVersion(ctx, q, &maxVersion); err != nil {
		return fmt.Errorf("verify schema_migrations version: %w", err)
	}
	if maxVersion < MinRequiredSchemaVersion {
		return fmt.Errorf(
			"数据库 migration 版本 < %d (当前=%d)，请先 apply 后再启动；database migration version below %d (current=%d), apply pending migrations before starting",
			MinRequiredSchemaVersion, maxVersion, MinRequiredSchemaVersion, maxVersion)
	}
	if err := verifySQLiteBaselineTables(ctx, q); err != nil {
		return err
	}
	if err := verifySQLiteRequiredColumns(ctx, q); err != nil {
		return err
	}
	if err := verifyNoActiveLegacyWakeups(ctx, q); err != nil {
		return err
	}
	if err := verifyContentSchemaColumnPolicy(ctx, q); err != nil {
		return err
	}
	if err := verifyResourceSchemaColumnPolicy(ctx, q); err != nil {
		return err
	}
	if err := verifySQLiteRequiredTriggers(ctx, q); err != nil {
		return err
	}
	if err := verifyTerminalProtocolViews(ctx, q); err != nil {
		return err
	}
	return nil
}

func verifySQLiteRequiredTriggers(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return fmt.Errorf("unsupported SQLite required trigger queryer %T", q)
	}
	for _, trigger := range requiredSQLiteTriggers() {
		var exists int
		if err := v.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = ?", trigger).Scan(&exists); err != nil {
			return fmt.Errorf("verify SQLite required trigger %s: %w", trigger, err)
		}
		if exists != 1 {
			return fmt.Errorf("SQLite baseline schema incomplete: missing required trigger: %s", trigger)
		}
	}
	return nil
}

type sqlContextQueryRow interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlContextQuery interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func querySchemaVersion(ctx context.Context, q any, dest *int) error {
	const query = "SELECT COALESCE(MAX(version), 0) FROM schema_migrations"
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return fmt.Errorf("unsupported schema version queryer %T", q)
	}
	return v.QueryRowContext(ctx, query).Scan(dest)
}

// verifySQLiteBaselineTables 拒绝只有 migration marker 或缺表的 SQLite 基线库。
func verifySQLiteBaselineTables(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return nil
	}
	missing, err := missingSQLiteBaselineTables(ctx, v)
	if err != nil {
		return fmt.Errorf("verify SQLite baseline schema tables: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("SQLite baseline schema incomplete: missing required table(s): %s; refusing marker-only or partial baseline database", strings.Join(missing, ", "))
	}
	return nil
}

func missingSQLiteBaselineTables(ctx context.Context, q sqlContextQueryRow) ([]string, error) {
	var missing []string
	for _, table := range requiredBaselineTables() {
		var exists int
		if err := q.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// verifyTerminalProtocolViews 确保 v121 已把旧协议降为只读 adapter，阻断旧 binary 混写。
func verifyTerminalProtocolViews(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQueryRow)
	if !ok {
		return fmt.Errorf("unsupported terminal protocol queryer %T", q)
	}
	for _, name := range requiredTerminalProtocolViews() {
		var objectType string
		if err := v.QueryRowContext(ctx,
			"SELECT type FROM sqlite_master WHERE name = ?",
			name,
		).Scan(&objectType); err != nil {
			return fmt.Errorf("verify terminal outcome protocol v121 object %s: %w", name, err)
		}
		if objectType != "view" {
			return fmt.Errorf("terminal outcome protocol v121 requires read-only view %s, got %s", name, objectType)
		}
	}
	return nil
}

// verifySQLiteRequiredColumns 用 PRAGMA table_info 校验生产代码依赖的关键列。
// 该检查补上 marker-only / 旧 schema 只建表不建列的启动缺口，缺列时必须阻断启动。
func verifySQLiteRequiredColumns(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQuery)
	if !ok {
		return fmt.Errorf("unsupported SQLite required column queryer %T", q)
	}
	missing, err := missingSQLiteRequiredColumns(ctx, v)
	if err != nil {
		return fmt.Errorf("verify SQLite baseline schema columns: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("SQLite baseline schema incomplete: missing required column(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// verifyNoActiveLegacyWakeups 在可执行 wakeup 仍带 pre-B09 标记时阻断启动。
// 源行必须由经过审查的 operator migration 或 quarantine 显式处理，启动绝不从旧字节推断分支。
func verifyNoActiveLegacyWakeups(ctx context.Context, q any) error {
	v, ok := q.(sqlContextQuery)
	if !ok {
		return fmt.Errorf("unsupported active legacy wakeup queryer %T", q)
	}
	rows, err := v.QueryContext(ctx, `
		SELECT id, dag_key, node_key, wakeup_kind, status
		FROM task_dag_wakeups
		WHERE payload_schema_version = 0
		  AND status IN ('pending', 'dispatching', 'sent')
		ORDER BY id`)
	if err != nil {
		return fmt.Errorf("verify active legacy task dag wakeups: %w", err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var (
			id      int64
			dagKey  string
			nodeKey string
			kind    string
			status  string
		)
		if err := rows.Scan(&id, &dagKey, &nodeKey, &kind, &status); err != nil {
			return fmt.Errorf("verify active legacy task dag wakeups: %w", err)
		}
		details = append(details, fmt.Sprintf("id=%d dag_key=%q node_key=%q wakeup_kind=%q status=%q", id, dagKey, nodeKey, kind, status))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("verify active legacy task dag wakeups: %w", err)
	}
	if len(details) == 0 {
		return nil
	}
	return fmt.Errorf(
		"active legacy task_dag_wakeups with payload_schema_version=0 require explicit operator migration/quarantine before startup; refusing implicit payload inference: %s",
		strings.Join(details, "; "))
}

// missingSQLiteRequiredColumns 汇总 schema gate 需要的缺失列列表。
// 同一张表只读取一次 PRAGMA，避免启动检查随 required 列数量线性重复扫表。
func missingSQLiteRequiredColumns(ctx context.Context, q sqlContextQuery) ([]string, error) {
	requiredColumns := requiredBaselineColumns()
	columnsByTable := make(map[string]map[string]struct{}, len(requiredColumns))
	missing := make([]string, 0)
	for _, required := range requiredColumns {
		columns, ok := columnsByTable[required.table]
		if !ok {
			var err error
			columns, err = sqliteTableColumns(ctx, q, required.table)
			if err != nil {
				return nil, err
			}
			columnsByTable[required.table] = columns
		}
		if _, ok := columns[required.column]; !ok {
			missing = append(missing, required.table+"."+required.column)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func sqliteTableColumns(ctx context.Context, q sqlContextQuery, table string) (map[string]struct{}, error) {
	rows, err := q.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

// registerLifecycle 在应用生命周期中执行 SQLite 迁移、schema gate 和关闭逻辑。
func registerLifecycle(lc fx.Lifecycle, logger *pkglogger.Logger, database *sql.DB, cfg *config.Config) {
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if cfg == nil {
				return fmt.Errorf("SQLite lifecycle config is nil")
			}
			if err := sqliteruntime.RunMigrations(ctx, database, sqliteMigrationsDir(cfg.ProjectRoot)); err != nil {
				return err
			}
			if err := VerifyMinSchemaVersion(ctx, database); err != nil {
				return err
			}
			if err := sqliteruntime.RestrictSidecarFilePermissions(cfg.SQLitePath); err != nil {
				return err
			}
			logger.Info("sqlite database ready")
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if err := database.Close(); err != nil {
				return err
			}
			logger.Info("sqlite database closed")
			return nil
		},
	})
}

func sqliteMigrationsDir(projectRoot string) string {
	return filepath.Join(strings.TrimSpace(projectRoot), "internal", "platform", "db", "sqlite", "migrations")
}

const migrationSplitSentinel = "-- SPLIT --"

// splitMigrationBody 按迁移脚本里的分段标记拆分 SQL。
// 未出现标记时保持原始脚本整体执行，避免改变旧迁移文件的事务语义。
func splitMigrationBody(body string) []string {
	var (
		out   []string
		part  strings.Builder
		found bool
	)
	for _, line := range strings.SplitAfter(body, "\n") {
		if strings.TrimSpace(line) == migrationSplitSentinel {
			found = true
			if segment := part.String(); strings.TrimSpace(segment) != "" {
				out = append(out, segment)
			}
			part.Reset()
			continue
		}
		part.WriteString(line)
	}
	if !found {
		return []string{body}
	}
	if segment := part.String(); strings.TrimSpace(segment) != "" {
		out = append(out, segment)
	}
	return out
}
