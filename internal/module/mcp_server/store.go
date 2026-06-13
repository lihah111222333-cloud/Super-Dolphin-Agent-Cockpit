package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MCPServerConfigStore interface {
	InsertServer(context.Context, StoreMCPServerConfigParams) (bool, error)
	ListServers(context.Context, string) (map[string]ServerConfig, error)
	DeleteServer(context.Context, string, string) (bool, error)
}

type StoreMCPServerConfigParams struct {
	WorkspaceRoot string
	Name          string
	Config        ServerConfig
}

type configStore struct {
	db platformdb.Queryable

	mu      sync.Mutex
	ensured bool
}

func NewMCPServerConfigStore(pool *pgxpool.Pool) MCPServerConfigStore {
	if pool == nil {
		return nil
	}
	return &configStore{db: pool}
}

func (s *configStore) InsertServer(ctx context.Context, params StoreMCPServerConfigParams) (bool, error) {
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
	tag, err := s.db.Exec(ctx, `
		INSERT INTO public.mcp_server_configs (
			workspace_root, name, transport, url, headers, updated_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, now())
		ON CONFLICT (workspace_root, name) DO NOTHING
	`, workspaceRoot, name, config.Transport, config.URL, string(headers))
	if err != nil {
		return false, wrapMCPServerStoreError(err, "insert")
	}
	return tag.RowsAffected() > 0, nil
}

func (s *configStore) ListServers(ctx context.Context, workspaceRoot string) (map[string]ServerConfig, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return nil, errMissingWorkspaceRoot
	}
	if err := s.ensureTable(ctx); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT name, transport, url, headers::text
		FROM public.mcp_server_configs
		WHERE workspace_root = $1
		ORDER BY name ASC
	`, workspaceRoot)
	if err != nil {
		return nil, wrapMCPServerStoreError(err, "list")
	}
	defer rows.Close()

	servers := make(map[string]ServerConfig)
	for rows.Next() {
		var name string
		var config ServerConfig
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
	tag, err := s.db.Exec(ctx, `
		DELETE FROM public.mcp_server_configs
		WHERE workspace_root = $1 AND name = $2
	`, workspaceRoot, name)
	if err != nil {
		return false, wrapMCPServerStoreError(err, "delete")
	}
	return tag.RowsAffected() > 0, nil
}

func (s *configStore) ensureTable(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errMCPServerStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ensured {
		return nil
	}
	if _, err := s.db.Exec(ctx, createMCPServerConfigsTableSQL); err != nil {
		return wrapMCPServerStoreError(err, "ensure_table")
	}
	s.ensured = true
	return nil
}

func normalizeStoreServerParams(params StoreMCPServerConfigParams) (string, string, ServerConfig, error) {
	workspaceRoot := strings.TrimSpace(params.WorkspaceRoot)
	if workspaceRoot == "" {
		return "", "", ServerConfig{}, errMissingWorkspaceRoot
	}
	name := strings.TrimSpace(params.Name)
	if name == "" || name != params.Name {
		return "", "", ServerConfig{}, errMissingServerName
	}
	config, err := normalizeServerConfig(name, params.Config)
	if err != nil {
		return "", "", ServerConfig{}, err
	}
	return workspaceRoot, name, config, nil
}

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

func wrapMCPServerStoreError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "mcp_server_config")
}

const createMCPServerConfigsTableSQL = `
CREATE TABLE IF NOT EXISTS public.mcp_server_configs (
	workspace_root text NOT NULL,
	name text NOT NULL,
	transport text NOT NULL,
	url text NOT NULL,
	headers jsonb NOT NULL DEFAULT '{}'::jsonb,
	created_at timestamp with time zone DEFAULT now() NOT NULL,
	updated_at timestamp with time zone DEFAULT now() NOT NULL,
	PRIMARY KEY (workspace_root, name),
	CHECK (workspace_root <> ''),
	CHECK (name <> ''),
	CHECK (transport <> ''),
	CHECK (url <> ''),
	CHECK (jsonb_typeof(headers) = 'object')
);
`

var _ MCPServerConfigStore = (*configStore)(nil)
