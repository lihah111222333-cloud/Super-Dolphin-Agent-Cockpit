CREATE TABLE shared_files_content_location_migration (
    path TEXT PRIMARY KEY,
    content TEXT NOT NULL,
    content_location TEXT NOT NULL DEFAULT 'inline' CHECK (content_location IN ('inline', 'disk')),
    updated_by TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

INSERT INTO shared_files_content_location_migration (
    path,
    content,
    content_location,
    updated_by,
    created_at,
    updated_at
)
SELECT
    path,
    content,
    'inline',
    updated_by,
    created_at,
    updated_at
FROM shared_files;

DROP TABLE shared_files;

ALTER TABLE shared_files_content_location_migration RENAME TO shared_files;

CREATE INDEX IF NOT EXISTS idx_shared_files_updated_at ON shared_files(updated_at DESC);
