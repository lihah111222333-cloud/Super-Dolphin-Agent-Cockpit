CREATE TABLE IF NOT EXISTS mcp_tool_lifecycle_states (
    workspace_root TEXT NOT NULL,
    server_name TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    lifecycle_state TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL,
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    PRIMARY KEY (workspace_root, server_name, tool_name),
    CHECK (workspace_root <> ''),
    CHECK (server_name <> ''),
    CHECK (tool_name <> ''),
    CHECK (lifecycle_state IN ('active', 'suspended', 'removed')),
    CHECK (source IN ('discovery', 'user', 'migration', 'system'))
);
