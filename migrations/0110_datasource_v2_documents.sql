CREATE TABLE IF NOT EXISTS datasource_v2_documents (
    id BIGSERIAL PRIMARY KEY,
    source_path TEXT NOT NULL,
    file_name TEXT NOT NULL,
    extension TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    content_hash TEXT,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    total_chars INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'importing',
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT datasource_v2_documents_source_path_key UNIQUE (source_path),
    CONSTRAINT datasource_v2_documents_source_path_nonempty CHECK (source_path <> ''),
    CONSTRAINT datasource_v2_documents_file_name_nonempty CHECK (file_name <> ''),
    CONSTRAINT datasource_v2_documents_size_nonnegative CHECK (size_bytes >= 0),
    CONSTRAINT datasource_v2_documents_chunk_count_nonnegative CHECK (chunk_count >= 0),
    CONSTRAINT datasource_v2_documents_total_chars_nonnegative CHECK (total_chars >= 0),
    CONSTRAINT datasource_v2_documents_status_valid CHECK (status IN ('importing', 'ready', 'failed')),
    CONSTRAINT datasource_v2_documents_ready_has_hash CHECK (status <> 'ready' OR content_hash IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS datasource_v2_text_chunks (
    id BIGSERIAL PRIMARY KEY,
    document_id BIGINT NOT NULL REFERENCES datasource_v2_documents(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    char_count INTEGER NOT NULL,
    byte_count INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT datasource_v2_text_chunks_document_chunk_key UNIQUE (document_id, chunk_index),
    CONSTRAINT datasource_v2_text_chunks_chunk_index_nonnegative CHECK (chunk_index >= 0),
    CONSTRAINT datasource_v2_text_chunks_content_nonempty CHECK (content <> ''),
    CONSTRAINT datasource_v2_text_chunks_char_count_positive CHECK (char_count > 0),
    CONSTRAINT datasource_v2_text_chunks_byte_count_positive CHECK (byte_count > 0)
);

CREATE INDEX IF NOT EXISTS datasource_v2_text_chunks_document_order_idx
    ON datasource_v2_text_chunks (document_id, chunk_index);
