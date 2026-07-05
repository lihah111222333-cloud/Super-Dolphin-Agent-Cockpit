package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

var (
	errMissingWorkspaceRoot        = errors.New("mcp_server: workspaceRoot is required")
	errMissingServerName           = errors.New("mcp_server: server name is required")
	errMissingServerTransport      = errors.New("mcp_server: transport is required")
	errUnsupportedTransport        = errors.New("mcp_server: unsupported transport")
	errMissingServerURL            = errors.New("mcp_server: url is required")
	errInvalidServerURL            = errors.New("mcp_server: invalid url")
	errMissingServerCommand        = errors.New("mcp_server: command is required")
	errMissingServerArg            = errors.New("mcp_server: arg is required")
	errMissingServerEnvName        = errors.New("mcp_server: env name is required")
	errMissingServerEnvValue       = errors.New("mcp_server: env value is required")
	errMissingHeaderName           = errors.New("mcp_server: header name is required")
	errMissingHeaderValue          = errors.New("mcp_server: header value is required")
	errInvalidConfigDocument       = errors.New("mcp_server: invalid config document")
	errMCPServerStoreNotConfigured = errors.New("mcp_server: config store is not configured")
	errMCPServerMigrationAnomaly   = errors.New("mcp_server: migration anomaly")
)

type configStore struct {
	db platformdb.Queryable

	mu      sync.Mutex
	ensured bool
}

// NewMCPServerConfigStore 创建 MCP server 配置存储；没有数据库句柄时返回 nil。
func NewMCPServerConfigStore(db platformdb.Queryable) contract.MCPServerConfigStore {
	if db == nil {
		return nil
	}
	return &configStore{db: db}
}

// InsertServer 插入单个 MCP server 配置，已存在时不覆盖并返回 inserted=false。
func (s *configStore) InsertServer(ctx context.Context, params contract.StoreMCPServerConfigParams) (bool, error) {
	workspaceRoot, name, config, err := normalizeStoreServerParams(params)
	if err != nil {
		return false, err
	}
	headers, err := encodeMCPServerHeaders(config.Headers)
	if err != nil {
		return false, err
	}
	args, err := encodeMCPServerArgs(config.Args)
	if err != nil {
		return false, err
	}
	env, err := encodeMCPServerEnv(config.Env)
	if err != nil {
		return false, err
	}
	if err := s.ensureTable(ctx); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (
			workspace_root, name, transport, url, headers, command, args, env, enabled, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
		ON CONFLICT (workspace_root, name) DO NOTHING
	`, workspaceRoot, name, config.Transport, config.URL, string(headers), config.Command, string(args), string(env), mcpServerEnabledInt(config.Enabled))
	if err != nil {
		return false, wrapMCPServerStoreError(err, "insert")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, wrapMCPServerStoreError(err, "insert.rows_affected")
	}
	return rowsAffected > 0, nil
}

// ListServers 读取指定工作区的 MCP server 配置，并校验落库内容仍然可用。
func (s *configStore) ListServers(ctx context.Context, workspaceRoot string) (map[string]contract.MCPServerConfig, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errMissingWorkspaceRoot
	}
	if err := s.ensureTable(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, transport, url, headers, command, args, env, enabled
		FROM mcp_server_configs
		WHERE workspace_root = ?
		ORDER BY name ASC
	`, workspaceRoot)
	if err != nil {
		return nil, wrapMCPServerStoreError(err, "list")
	}
	defer rows.Close()

	servers := make(map[string]contract.MCPServerConfig)
	for rows.Next() {
		var name string
		var config contract.MCPServerConfig
		var headersJSON, argsJSON, envJSON string
		var enabled int
		if err := rows.Scan(&name, &config.Transport, &config.URL, &headersJSON, &config.Command, &argsJSON, &envJSON, &enabled); err != nil {
			return nil, wrapMCPServerStoreError(err, "list.scan")
		}
		normalized, err := decodeStoredMCPServerConfig(name, config, headersJSON, argsJSON, envJSON, enabled)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
		}
		servers[name] = normalized
	}
	if err := rows.Err(); err != nil {
		return nil, wrapMCPServerStoreError(err, "list.rows")
	}
	return servers, nil
}

func decodeStoredMCPServerConfig(
	name string,
	config contract.MCPServerConfig,
	headersJSON string,
	argsJSON string,
	envJSON string,
	enabled int,
) (contract.MCPServerConfig, error) {
	headers, err := decodeMCPServerHeaders(headersJSON)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	config.Headers = headers
	args, err := decodeMCPServerArgs(argsJSON)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	config.Args = args
	env, err := decodeMCPServerEnv(envJSON)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	config.Env = env
	config.Enabled = boolPtr(enabled != 0)
	return normalizeServerConfig(name, config)
}

// DeleteServer 删除指定工作区的 MCP server 配置，返回是否真的删除了记录。
func (s *configStore) DeleteServer(ctx context.Context, workspaceRoot, name string) (bool, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	name = strings.TrimSpace(name)
	if workspaceRoot == "" {
		return false, errMissingWorkspaceRoot
	}
	if name == "" {
		return false, errMissingServerName
	}
	if err := s.ensureTable(ctx); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM mcp_server_configs
		WHERE workspace_root = ? AND name = ?
	`, workspaceRoot, name)
	if err != nil {
		return false, wrapMCPServerStoreError(err, "delete")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, wrapMCPServerStoreError(err, "delete.rows_affected")
	}
	return rowsAffected > 0, nil
}

// SetServerEnabled 更新指定 MCP server 的开启状态，关闭时保留配置供后续按需恢复。
func (s *configStore) SetServerEnabled(ctx context.Context, workspaceRoot, name string, enabled bool) (bool, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	name = strings.TrimSpace(name)
	if workspaceRoot == "" {
		return false, errMissingWorkspaceRoot
	}
	if name == "" {
		return false, errMissingServerName
	}
	if err := s.ensureTable(ctx); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE mcp_server_configs
		SET enabled = ?, updated_at = CAST(strftime('%s','now') AS INTEGER) * 1000
		WHERE workspace_root = ? AND name = ?
	`, boolToInt(enabled), workspaceRoot, name)
	if err != nil {
		return false, wrapMCPServerStoreError(err, "set_enabled")
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, wrapMCPServerStoreError(err, "set_enabled.rows_affected")
	}
	return rowsAffected > 0, nil
}

// ensureTable 首次使用时建表，并用锁避免并发请求重复建表。
func (s *configStore) ensureTable(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errMCPServerStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensured {
		return nil
	}
	if err := s.repairMCPServerConfigMigrationState(ctx); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, createMCPServerConfigsTableSQL); err != nil {
		return wrapMCPServerStoreError(err, "ensure_table")
	}
	if err := s.ensureTableShape(ctx); err != nil {
		return err
	}
	s.ensured = true
	return nil
}

// ensureTableShape 把旧的 HTTP-only 表升级为可保存 stdio 命令的形状。
// 旧表带有 url 非空约束，不能靠 ALTER COLUMN 修改，因此发现缺列时直接重建并保留现有 HTTP 行。
func (s *configStore) ensureTableShape(ctx context.Context) error {
	columns, err := s.mcpServerConfigColumns(ctx)
	if err != nil {
		return err
	}
	for _, name := range []string{"command", "args", "env"} {
		if !columns[name] {
			return s.rebuildLegacyTable(ctx)
		}
	}
	if !columns["enabled"] {
		return s.addMCPServerEnabledColumn(ctx)
	}
	return nil
}

func (s *configStore) mcpServerConfigColumns(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(mcp_server_configs)`)
	if err != nil {
		return nil, wrapMCPServerStoreError(err, "columns")
	}
	defer rows.Close()
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, wrapMCPServerStoreError(err, "columns.scan")
		}
		columns[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrapMCPServerStoreError(err, "columns.rows")
	}
	return columns, nil
}

// repairMCPServerConfigMigrationState 在建表前处理上次重建遗留的 _next 表。
// 主表缺失时直接恢复 rename；主表和 _next 同时存在代表迁移中断，必须 fail-fast，避免把 _next 中的配置读成空配置。
func (s *configStore) repairMCPServerConfigMigrationState(ctx context.Context) error {
	mainExists, err := s.mcpServerConfigTableExists(ctx, "mcp_server_configs")
	if err != nil {
		return err
	}
	nextExists, err := s.mcpServerConfigTableExists(ctx, "mcp_server_configs_next")
	if err != nil {
		return err
	}
	if !nextExists {
		return nil
	}
	if mainExists {
		return fmt.Errorf("%w: both mcp_server_configs and mcp_server_configs_next exist", errMCPServerMigrationAnomaly)
	}
	return s.runMCPServerMigrationTx(ctx, func(q platformdb.Queryable) error {
		if _, err := q.ExecContext(ctx, `ALTER TABLE mcp_server_configs_next RENAME TO mcp_server_configs`); err != nil {
			return wrapMCPServerStoreError(err, "repair_next.rename")
		}
		return nil
	})
}

func (s *configStore) mcpServerConfigTableExists(ctx context.Context, table string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM sqlite_master
		WHERE type = 'table' AND name = ?
	`, table).Scan(&count)
	if err != nil {
		return false, wrapMCPServerStoreError(err, "table_exists")
	}
	return count > 0, nil
}

func (s *configStore) rebuildLegacyTable(ctx context.Context) error {
	return s.runMCPServerMigrationTx(ctx, func(q platformdb.Queryable) error {
		for _, stmt := range []string{
			`DROP TABLE IF EXISTS mcp_server_configs_next`,
			createMCPServerConfigsNextTableSQL,
			`INSERT INTO mcp_server_configs_next (
				workspace_root, name, transport, url, headers, command, args, env, created_at, updated_at
			)
			SELECT workspace_root, name, transport, url, headers, '', '[]', '{}', created_at, updated_at
			FROM mcp_server_configs`,
			`DROP TABLE mcp_server_configs`,
			`ALTER TABLE mcp_server_configs_next RENAME TO mcp_server_configs`,
		} {
			if _, err := q.ExecContext(ctx, stmt); err != nil {
				return wrapMCPServerStoreError(err, "migrate_stdio")
			}
		}
		return nil
	})
}

type mcpServerMigrationTxRunner interface {
	withMCPServerMigrationTx(context.Context, func(platformdb.Queryable) error) error
}

// runMCPServerMigrationTx 用同一个 SQLite 事务包住 DDL 重建，避免 drop/rename 中途失败留下半迁移状态。
func (s *configStore) runMCPServerMigrationTx(ctx context.Context, fn func(platformdb.Queryable) error) error {
	if runner, ok := s.db.(mcpServerMigrationTxRunner); ok {
		return runner.withMCPServerMigrationTx(ctx, fn)
	}
	beginner, ok := s.db.(interface {
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return fmt.Errorf("%w: database does not support transactions", errMCPServerMigrationAnomaly)
	}
	tx, err := beginner.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return wrapMCPServerStoreError(err, "migration.begin")
	}
	if err := fn(tx); err != nil {
		return rollbackMCPServerMigrationTx(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return wrapMCPServerStoreError(err, "migration.commit")
	}
	return nil
}

func rollbackMCPServerMigrationTx(tx *sql.Tx, cause error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *configStore) addMCPServerEnabledColumn(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `ALTER TABLE mcp_server_configs ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1))`)
	if err != nil {
		return wrapMCPServerStoreError(err, "migrate_enabled")
	}
	return nil
}

// normalizeStoreServerParams 清理并校验写入参数，避免把不可用配置落库。
func normalizeStoreServerParams(params contract.StoreMCPServerConfigParams) (string, string, contract.MCPServerConfig, error) {
	workspaceRoot := strings.TrimSpace(params.WorkspaceRoot)
	if workspaceRoot == "" {
		return "", "", contract.MCPServerConfig{}, errMissingWorkspaceRoot
	}
	name := strings.TrimSpace(params.Name)
	if name == "" || name != params.Name {
		return "", "", contract.MCPServerConfig{}, errMissingServerName
	}
	config, err := normalizeServerConfig(name, params.Config)
	if err != nil {
		return "", "", contract.MCPServerConfig{}, err
	}
	return workspaceRoot, name, config, nil
}

// normalizeServerConfig 校验 MCP server 配置，按 transport 分别清理 HTTP 和 stdio 字段。
func normalizeServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	switch transport {
	case "http":
		return normalizeHTTPServerConfig(name, config)
	case "stdio":
		return normalizeStdioServerConfig(name, config)
	default:
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedTransport, transport)
	}
}

func normalizeHTTPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errMissingServerURL, name)
	}
	if err := validateHTTPURL(rawURL); err != nil {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", err, rawURL)
	}
	headers, err := normalizeHeaders(name, config.Headers)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	return contract.MCPServerConfig{
		Transport: "http",
		URL:       rawURL,
		Headers:   headers,
		Enabled:   normalizeMCPServerEnabled(config.Enabled),
	}, nil
}

func normalizeStdioServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errMissingServerCommand, name)
	}
	args, err := normalizeStringList(config.Args, errMissingServerArg, name)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	env, err := normalizeStringMap(config.Env, errMissingServerEnvName, errMissingServerEnvValue, name)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	if err := contract.DefaultRuntimeMCPPolicy().ValidateRuntimeStdioCommand(command, args, ""); err != nil {
		return contract.MCPServerConfig{}, fmt.Errorf("mcp_server: %w: %s", err, name)
	}
	return contract.MCPServerConfig{
		Transport: "stdio",
		Command:   command,
		Args:      args,
		Env:       env,
		Enabled:   normalizeMCPServerEnabled(config.Enabled),
	}, nil
}

// normalizeHeaders 清理 header 名和值，空 map 统一写成 nil。
func normalizeHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%w: %s", errMissingHeaderName, serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s.%s", errMissingHeaderValue, serverName, name)
		}
		headers[name] = value
	}
	return headers, nil
}

func normalizeStringList(input []string, emptyErr error, label string) ([]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(input))
	for _, rawValue := range input {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s", emptyErr, label)
		}
		out = append(out, value)
	}
	return out, nil
}

func normalizeStringMap(input map[string]string, emptyKeyErr, emptyValueErr error, label string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%w: %s", emptyKeyErr, label)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s.%s", emptyValueErr, label, name)
		}
		out[name] = value
	}
	return out, nil
}

func normalizeMCPServerEnabled(enabled *bool) *bool {
	if enabled == nil {
		return boolPtr(true)
	}
	return boolPtr(*enabled)
}

func mcpServerEnabledInt(enabled *bool) int {
	if enabled == nil || *enabled {
		return 1
	}
	return 0
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func boolPtr(value bool) *bool {
	return &value
}

// validateHTTPURL 确认 URL 是 HTTP/HTTPS 且带 host，避免写入运行时不可连接的配置。
func validateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errInvalidServerURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errInvalidServerURL
	}
	if parsed.Host == "" {
		return errInvalidServerURL
	}
	return nil
}

// encodeMCPServerHeaders 把 header map 规范化后写成 JSON 字符串。
func encodeMCPServerHeaders(headers map[string]string) ([]byte, error) {
	normalized, err := normalizeHeaders("headers", headers)
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server headers: %w", err)
	}
	return raw, nil
}

func encodeMCPServerArgs(args []string) ([]byte, error) {
	normalized, err := normalizeStringList(args, errMissingServerArg, "args")
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return []byte("[]"), nil
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server args: %w", err)
	}
	return raw, nil
}

func encodeMCPServerEnv(env map[string]string) ([]byte, error) {
	normalized, err := normalizeStringMap(env, errMissingServerEnvName, errMissingServerEnvValue, "env")
	if err != nil {
		return nil, err
	}
	if len(normalized) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server env: %w", err)
	}
	return raw, nil
}

// decodeMCPServerHeaders 读取 JSON 文本并重新校验 header 内容。
func decodeMCPServerHeaders(raw string) (map[string]string, error) {
	var headers map[string]string
	if err := json.Unmarshal([]byte(raw), &headers); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	normalized, err := normalizeHeaders("headers", headers)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	return normalized, nil
}

func decodeMCPServerArgs(raw string) ([]string, error) {
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	normalized, err := normalizeStringList(args, errMissingServerArg, "args")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	return normalized, nil
}

func decodeMCPServerEnv(raw string) (map[string]string, error) {
	var env map[string]string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	normalized, err := normalizeStringMap(env, errMissingServerEnvName, errMissingServerEnvValue, "env")
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	return normalized, nil
}

// wrapMCPServerStoreError 统一包装数据库错误，保留具体操作名便于排查。
func wrapMCPServerStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "mcp_server_config")
}

const createMCPServerConfigsTableSQL = `
CREATE TABLE IF NOT EXISTS mcp_server_configs (
	workspace_root TEXT NOT NULL,
	name TEXT NOT NULL,
	transport TEXT NOT NULL,
	url TEXT NOT NULL DEFAULT '',
	headers TEXT NOT NULL DEFAULT '{}',
	command TEXT NOT NULL DEFAULT '',
	args TEXT NOT NULL DEFAULT '[]',
	env TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (transport <> ''),
	CHECK (transport IN ('http', 'stdio')),
	CHECK ((transport = 'http' AND url <> '') OR (transport = 'stdio' AND command <> '')),
	CHECK (headers <> ''),
	CHECK (args <> ''),
	CHECK (env <> ''),
	CHECK (enabled IN (0, 1))
);
`

var createMCPServerConfigsNextTableSQL = strings.Replace(createMCPServerConfigsTableSQL, "CREATE TABLE IF NOT EXISTS mcp_server_configs", "CREATE TABLE mcp_server_configs_next", 1)

var _ contract.MCPServerConfigStore = (*configStore)(nil)
