-- name: UpsertMCPToolLifecycleState :one
INSERT INTO mcp_tool_lifecycle_states (
    workspace_root,
    server_name,
    tool_name,
    lifecycle_state,
    reason,
    source,
    updated_by,
    updated_at
)
VALUES (
    sqlc.arg(workspace_root),
    sqlc.arg(server_name),
    sqlc.arg(tool_name),
    sqlc.arg(lifecycle_state),
    sqlc.arg(reason),
    sqlc.arg(source),
    sqlc.arg(updated_by),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
ON CONFLICT (workspace_root, server_name, tool_name) DO UPDATE
SET lifecycle_state = EXCLUDED.lifecycle_state,
    reason = EXCLUDED.reason,
    source = EXCLUDED.source,
    updated_by = EXCLUDED.updated_by,
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000)
RETURNING workspace_root, server_name, tool_name, lifecycle_state, reason, source, updated_by, created_at, updated_at;

-- name: InsertMCPToolLifecycleStateIfAbsent :execrows
INSERT INTO mcp_tool_lifecycle_states (
    workspace_root,
    server_name,
    tool_name,
    lifecycle_state,
    reason,
    source,
    updated_by,
    updated_at
)
VALUES (
    sqlc.arg(workspace_root),
    sqlc.arg(server_name),
    sqlc.arg(tool_name),
    sqlc.arg(lifecycle_state),
    sqlc.arg(reason),
    sqlc.arg(source),
    sqlc.arg(updated_by),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
ON CONFLICT (workspace_root, server_name, tool_name) DO NOTHING;

-- name: GetMCPToolLifecycleState :one
SELECT workspace_root, server_name, tool_name, lifecycle_state, reason, source, updated_by, created_at, updated_at
FROM mcp_tool_lifecycle_states
WHERE workspace_root = sqlc.arg(workspace_root)
  AND server_name = sqlc.arg(server_name)
  AND tool_name = sqlc.arg(tool_name);

-- name: ListMCPToolLifecycleStatesByServer :many
SELECT workspace_root, server_name, tool_name, lifecycle_state, reason, source, updated_by, created_at, updated_at
FROM mcp_tool_lifecycle_states
WHERE workspace_root = sqlc.arg(workspace_root)
  AND server_name = sqlc.arg(server_name)
ORDER BY tool_name ASC;
