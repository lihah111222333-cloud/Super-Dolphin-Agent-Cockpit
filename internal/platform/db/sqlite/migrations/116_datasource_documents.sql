CREATE TABLE IF NOT EXISTS datasource_documents (
    workspace_root TEXT NOT NULL,
    name TEXT NOT NULL,
    extension TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
    stored_path TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    PRIMARY KEY (workspace_root, name),
    CHECK (workspace_root <> ''),
    CHECK (name <> ''),
    CHECK (extension <> ''),
    CHECK (stored_path <> ''),
    CHECK (content <> '')
);

CREATE INDEX IF NOT EXISTS idx_datasource_documents_workspace_name
    ON datasource_documents(workspace_root, name);
