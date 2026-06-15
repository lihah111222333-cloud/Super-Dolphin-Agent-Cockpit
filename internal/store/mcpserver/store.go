package mcpserver

import (
	"context"
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
	errMissingHeaderName           = errors.New("mcp_server: header name is required")
	errMissingHeaderValue          = errors.New("mcp_server: header value is required")
	errInvalidConfigDocument       = errors.New("mcp_server: invalid config document")
	errMCPServerStoreNotConfigured = errors.New("mcp_server: config store is not configured")
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
	if err := s.ensureTable(ctx); err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO mcp_server_configs (
			workspace_root, name, transport, url, headers, updated_at
		) VALUES (?, ?, ?, ?, ?, CAST(strftime('%s','now') AS INTEGER) * 1000)
		ON CONFLICT (workspace_root, name) DO NOTHING
	`, workspaceRoot, name, config.Transport, config.URL, string(headers))
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
		SELECT name, transport, url, headers
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
		var headersJSON string
		if err := rows.Scan(&name, &config.Transport, &config.URL, &headersJSON); err != nil {
			return nil, wrapMCPServerStoreError(err, "list.scan")
		}
		headers, err := decodeMCPServerHeaders(headersJSON)
		if err != nil {
			return nil, err
		}
		config.Headers = headers
		normalized, err := normalizeServerConfig(name, config)
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
	if _, err := s.db.ExecContext(ctx, createMCPServerConfigsTableSQL); err != nil {
		return wrapMCPServerStoreError(err, "ensure_table")
	}
	s.ensured = true
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

// normalizeServerConfig 校验 MCP server 配置，只允许当前支持的 HTTP/HTTPS 传输。
func normalizeServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	if transport != "http" {
		return contract.MCPServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedTransport, transport)
	}
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
		Transport: transport,
		URL:       rawURL,
		Headers:   headers,
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

// wrapMCPServerStoreError 统一包装数据库错误，保留具体操作名便于排查。
func wrapMCPServerStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "mcp_server_config")
}

const createMCPServerConfigsTableSQL = `
CREATE TABLE IF NOT EXISTS mcp_server_configs (
	workspace_root TEXT NOT NULL,
	name TEXT NOT NULL,
	transport TEXT NOT NULL,
	url TEXT NOT NULL,
	headers TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (transport <> ''),
	CHECK (url <> ''),
	CHECK (headers <> '')
);
`

var _ contract.MCPServerConfigStore = (*configStore)(nil)
