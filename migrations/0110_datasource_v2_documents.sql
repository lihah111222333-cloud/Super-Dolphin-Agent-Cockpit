CREATE TABLE IF NOT EXISTS datasource_v2_documents (
    id INTEGER PRIMARY KEY,
    source_path TEXT NOT NULL,
    file_name TEXT NOT NULL,
    extension TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    content_hash TEXT,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    total_chars INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'importing' CHECK(status IN ('importing', 'ready', 'failed')),
    error_message TEXT,
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    UNIQUE(source_path),
    CHECK(source_path <> ''),
    CHECK(file_name <> ''),
    CHECK(size_bytes >= 0),
    CHECK(chunk_count >= 0),
    CHECK(total_chars >= 0),
    CHECK(status <> 'ready' OR content_hash IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS datasource_v2_text_chunks (
    id INTEGER PRIMARY KEY,
    document_id INTEGER NOT NULL REFERENCES datasource_v2_documents(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    char_count INTEGER NOT NULL,
    byte_count INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
    UNIQUE(document_id, chunk_index),
    CHECK(chunk_index >= 0),
    CHECK(content <> ''),
    CHECK(char_count > 0),
    CHECK(byte_count > 0)
);

CREATE INDEX IF NOT EXISTS idx_datasource_v2_text_chunks_document_order
    ON datasource_v2_text_chunks(document_id, chunk_index);
