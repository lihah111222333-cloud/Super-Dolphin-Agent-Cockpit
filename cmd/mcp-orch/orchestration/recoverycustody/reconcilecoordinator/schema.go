package reconcilecoordinator

const schemaSQL = `
CREATE TABLE coordinator_meta (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    schema_version INTEGER NOT NULL CHECK (schema_version = 1),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE coordinator_owner (
    singleton INTEGER PRIMARY KEY NOT NULL CHECK (singleton = 1),
    owner_id TEXT NOT NULL CHECK (length(owner_id) BETWEEN 1 AND 256 AND trim(owner_id) = owner_id),
    owner_epoch INTEGER NOT NULL CHECK (owner_epoch > 0),
    owner_token TEXT NOT NULL CHECK (length(owner_token) = 64 AND owner_token NOT GLOB '*[^0-9a-f]*'),
    lease_expires_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE reconcile_jobs (
    job_id TEXT PRIMARY KEY NOT NULL CHECK (length(job_id) BETWEEN 1 AND 256 AND trim(job_id) = job_id),
    operation_id TEXT NOT NULL UNIQUE CHECK (length(operation_id) BETWEEN 1 AND 128 AND trim(operation_id) = operation_id),
    handle_json TEXT NOT NULL CHECK (json_valid(handle_json) AND json_type(handle_json) = 'object'),
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'retry_wait', 'succeeded', 'indeterminate')),
    attempts INTEGER NOT NULL CHECK (attempts >= 0),
    next_run_at TEXT NOT NULL,
    owner_id TEXT,
    owner_epoch INTEGER,
    claim_token TEXT,
    lease_expires_at TEXT,
    last_status TEXT CHECK (last_status IS NULL OR length(last_status) BETWEEN 1 AND 64),
    last_error TEXT CHECK (last_error IS NULL OR length(last_error) <= 2048),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK ((state IN ('queued', 'retry_wait') AND claim_token IS NULL AND owner_id IS NULL AND owner_epoch IS NULL AND lease_expires_at IS NULL)
        OR (state = 'running' AND claim_token IS NOT NULL AND owner_id IS NOT NULL AND owner_epoch IS NOT NULL AND lease_expires_at IS NOT NULL)
        OR (state IN ('succeeded', 'indeterminate') AND claim_token IS NULL AND owner_id IS NULL AND owner_epoch IS NULL AND lease_expires_at IS NULL))
) STRICT;
CREATE TABLE reconcile_journal (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 128 AND trim(event) = event),
    state TEXT NOT NULL,
    claim_token TEXT,
    detail TEXT NOT NULL CHECK (length(detail) <= 2048),
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES reconcile_jobs(job_id)
) STRICT;
CREATE TABLE reconcile_audit (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    event TEXT NOT NULL CHECK (length(event) BETWEEN 1 AND 128 AND trim(event) = event),
    state TEXT NOT NULL,
    status TEXT NOT NULL CHECK (length(status) <= 64),
    detail TEXT NOT NULL CHECK (length(detail) <= 2048),
    owner_id TEXT NOT NULL,
    owner_epoch INTEGER NOT NULL CHECK (owner_epoch > 0),
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES reconcile_jobs(job_id)
) STRICT;
CREATE INDEX reconcile_jobs_due_idx ON reconcile_jobs(state, next_run_at);
CREATE TRIGGER reconcile_journal_no_update BEFORE UPDATE ON reconcile_journal BEGIN SELECT RAISE(ABORT, 'reconcile journal is append-only'); END;
CREATE TRIGGER reconcile_journal_no_delete BEFORE DELETE ON reconcile_journal BEGIN SELECT RAISE(ABORT, 'reconcile journal is append-only'); END;
CREATE TRIGGER reconcile_audit_no_update BEFORE UPDATE ON reconcile_audit BEGIN SELECT RAISE(ABORT, 'reconcile audit is append-only'); END;
CREATE TRIGGER reconcile_audit_no_delete BEFORE DELETE ON reconcile_audit BEGIN SELECT RAISE(ABORT, 'reconcile audit is append-only'); END;
`
