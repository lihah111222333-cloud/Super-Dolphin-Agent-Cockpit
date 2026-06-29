CREATE TABLE IF NOT EXISTS mcp_tool_lifecycle (
    workspace_root TEXT NOT NULL,
    server_name TEXT NOT NULL,
    manifest_name TEXT NOT NULL DEFAULT '',
    tool_name TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'enabled',
    reason TEXT NOT NULL DEFAULT '',
    replacement_tool TEXT NOT NULL DEFAULT '',
    last_seen_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (workspace_root, server_name, tool_name),
    CHECK (workspace_root <> ''),
    CHECK (server_name <> ''),
    CHECK (tool_name <> ''),
    CHECK (state IN ('enabled', 'disabled', 'suspended', 'removed')),
    CHECK (last_seen_at >= 0),
    CHECK (created_at >= 0),
    CHECK (updated_at >= 0)
);

CREATE INDEX IF NOT EXISTS idx_mcp_tool_lifecycle_server
    ON mcp_tool_lifecycle(workspace_root, server_name, state, updated_at DESC);
