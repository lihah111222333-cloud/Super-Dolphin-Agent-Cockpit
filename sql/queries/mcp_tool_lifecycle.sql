-- name: GetMCPToolLifecycle :one
SELECT workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at
FROM mcp_tool_lifecycle
WHERE workspace_root = ? AND server_name = ? AND tool_name = ?;

-- name: ListMCPToolLifecycle :many
SELECT workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at
FROM mcp_tool_lifecycle
WHERE workspace_root = ? AND server_name = ?
ORDER BY tool_name ASC;

-- name: UpsertMCPToolLifecycle :one
INSERT INTO mcp_tool_lifecycle (
    workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at
) VALUES (
    sqlc.arg(workspace_root),
    sqlc.arg(server_name),
    sqlc.arg(manifest_name),
    sqlc.arg(tool_name),
    sqlc.arg(state),
    sqlc.arg(reason),
    sqlc.arg(replacement_tool),
    sqlc.arg(now),
    sqlc.arg(now),
    sqlc.arg(now)
)
ON CONFLICT (workspace_root, server_name, tool_name) DO UPDATE
SET manifest_name = EXCLUDED.manifest_name,
    state = EXCLUDED.state,
    reason = EXCLUDED.reason,
    replacement_tool = EXCLUDED.replacement_tool,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = EXCLUDED.updated_at
RETURNING workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at;

-- name: BackfillMCPToolLifecycle :one
INSERT INTO mcp_tool_lifecycle (
    workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at
) VALUES (
    sqlc.arg(workspace_root),
    sqlc.arg(server_name),
    sqlc.arg(manifest_name),
    sqlc.arg(tool_name),
    'enabled',
    '',
    '',
    sqlc.arg(now),
    sqlc.arg(now),
    sqlc.arg(now)
)
ON CONFLICT (workspace_root, server_name, tool_name) DO UPDATE
SET manifest_name = CASE
        WHEN EXCLUDED.manifest_name <> '' THEN EXCLUDED.manifest_name
        ELSE mcp_tool_lifecycle.manifest_name
    END,
    last_seen_at = EXCLUDED.last_seen_at,
    updated_at = EXCLUDED.updated_at
RETURNING workspace_root, server_name, manifest_name, tool_name, state, reason, replacement_tool, last_seen_at, created_at, updated_at;
