-- B09 wakeup payload version floor.
-- Existing rows are deliberately retained with payload_schema_version=0. No
-- historical payload is rewritten or inferred by this migration.
ALTER TABLE task_dag_wakeups
ADD COLUMN payload_schema_version INTEGER NOT NULL DEFAULT 0
CHECK(payload_schema_version IN (0, 1));

-- Quarantine is intentionally independent from task_dag_wakeups: an operator
-- may record a legacy row even after its source run has been deleted. The
-- source row itself remains untouched until an explicit, reviewed migration.
CREATE TABLE orchestration_legacy_wakeup_quarantine (
    quarantine_id TEXT PRIMARY KEY NOT NULL
        CHECK(length(quarantine_id) BETWEEN 1 AND 256
            AND trim(quarantine_id) = quarantine_id AND instr(quarantine_id, char(0)) = 0
            AND quarantine_id LIKE 'legacy-wakeup-quarantine:v1:%'),
    source_wakeup_id INTEGER NOT NULL CHECK(source_wakeup_id > 0),
    source_payload_digest TEXT NOT NULL CHECK(
        length(source_payload_digest) = 64
        AND source_payload_digest NOT GLOB '*[^0-9a-f]*'
    ),
    wakeup_kind TEXT NOT NULL CHECK(length(trim(wakeup_kind)) > 0),
    target_agent_id TEXT NOT NULL CHECK(length(trim(target_agent_id)) > 0),
    -- Preserve exact legacy bytes, including malformed JSON. The Go planner
    -- owns syntax/deep-field validation; quarantine is never executable.
    prompt_payload TEXT NOT NULL CHECK(
        typeof(prompt_payload) = 'text'
        AND length(CAST(prompt_payload AS BLOB)) BETWEEN 1 AND 262144
        AND instr(prompt_payload, char(0)) = 0
    ),
    reason TEXT NOT NULL CHECK(length(trim(reason)) > 0),
    source_schema_version INTEGER NOT NULL CHECK(source_schema_version = 0),
    schema_version INTEGER NOT NULL CHECK(schema_version = 1),
    created_at INTEGER NOT NULL CHECK(created_at > 0),
    UNIQUE(source_wakeup_id, source_payload_digest)
) STRICT;

CREATE INDEX idx_orchestration_legacy_wakeup_quarantine_source
    ON orchestration_legacy_wakeup_quarantine(source_wakeup_id);

CREATE TRIGGER trg_orchestration_legacy_wakeup_quarantine_no_update
BEFORE UPDATE ON orchestration_legacy_wakeup_quarantine
BEGIN
    SELECT RAISE(ABORT, 'legacy wakeup quarantine is immutable');
END;

CREATE TRIGGER trg_orchestration_legacy_wakeup_quarantine_no_delete
BEFORE DELETE ON orchestration_legacy_wakeup_quarantine
BEGIN
    SELECT RAISE(ABORT, 'legacy wakeup quarantine is immutable');
END;

-- SQLite enforces the stable envelope and row identity. The Go decoder
-- remains authoritative for duplicate keys, unknown fields and deep limits.
-- Every branch is a CASE arm: unlike `NOT (predicate)`, CASE never yields a
-- NULL WHEN result that could silently bypass a trigger in SQLite.
CREATE TRIGGER trg_task_dag_wakeups_v134_insert
BEFORE INSERT ON task_dag_wakeups
WHEN COALESCE(NEW.payload_schema_version, -1) = 1
 AND COALESCE((CASE
    WHEN NEW.wakeup_kind IN ('node_start', 'manual_dispatch') THEN
        json_valid(NEW.prompt_payload)
        AND NOT EXISTS (SELECT 1 FROM json_tree(NEW.prompt_payload) WHERE type = 'null')
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.wakeup_payload_kind') = 'text'
        AND json_extract(NEW.prompt_payload, '$.wakeup_payload_kind') = 'dag_downstream'
        AND json_type(NEW.prompt_payload, '$.schema_version') = 'integer'
        AND json_extract(NEW.prompt_payload, '$.schema_version') = 1
        AND json_type(NEW.prompt_payload, '$.agent_id') = 'text'
        AND length(trim(json_extract(NEW.prompt_payload, '$.agent_id'))) > 0
        AND json_extract(NEW.prompt_payload, '$.agent_id') = trim(json_extract(NEW.prompt_payload, '$.agent_id'))
        AND length(trim(COALESCE(NEW.target_agent_id, ''))) > 0
        AND NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(json_extract(NEW.prompt_payload, '$.agent_id')) = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    WHEN NEW.wakeup_kind = 'start' THEN
        json_valid(NEW.prompt_payload)
        AND NOT EXISTS (SELECT 1 FROM json_tree(NEW.prompt_payload) WHERE type = 'null')
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.wakeup_payload_kind') = 'text'
        AND json_extract(NEW.prompt_payload, '$.wakeup_payload_kind') = 'legacy_launch'
        AND json_type(NEW.prompt_payload, '$.schema_version') = 'integer'
        AND json_extract(NEW.prompt_payload, '$.schema_version') = 1
        AND json_type(NEW.prompt_payload, '$.agent_id') = 'text'
        AND length(trim(json_extract(NEW.prompt_payload, '$.agent_id'))) > 0
        AND json_extract(NEW.prompt_payload, '$.agent_id') = trim(json_extract(NEW.prompt_payload, '$.agent_id'))
        AND length(trim(COALESCE(NEW.target_agent_id, ''))) > 0
        AND NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(json_extract(NEW.prompt_payload, '$.agent_id')) = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.dag_key, '')) = ''
        AND trim(COALESCE(NEW.node_key, '')) = ''
        AND NEW.run_id IS NULL
    WHEN NEW.wakeup_kind = 'turn_complete_retry' THEN
        NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.target_agent_id, '')) = 'mcp-orch'
        AND json_valid(NEW.prompt_payload)
        AND json_type(NEW.prompt_payload) = 'object'
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    WHEN NEW.wakeup_kind = 'terminal_failure_compensation' THEN
        NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.target_agent_id, '')) = 'mcp-orch'
        AND json_valid(NEW.prompt_payload)
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.reason') = 'text'
        AND json_type(NEW.prompt_payload, '$.store_error') = 'text'
        AND (json_type(NEW.prompt_payload, '$.fail_fast') = 'true'
            OR json_type(NEW.prompt_payload, '$.fail_fast') = 'false')
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    ELSE 0
 END), 0) = 0
BEGIN
    SELECT RAISE(ABORT, 'task dag wakeup v1 envelope is invalid');
END;

CREATE TRIGGER trg_task_dag_wakeups_v134_insert_version
BEFORE INSERT ON task_dag_wakeups
WHEN COALESCE(NEW.payload_schema_version, -1) = 0
BEGIN
    SELECT RAISE(ABORT, 'task dag wakeup payload_schema_version must be 1 for new rows');
END;

CREATE TRIGGER trg_task_dag_wakeups_v134_update
BEFORE UPDATE OF payload_schema_version, dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload ON task_dag_wakeups
WHEN COALESCE(NEW.payload_schema_version, -1) = 1
 AND COALESCE((CASE
    WHEN NEW.wakeup_kind IN ('node_start', 'manual_dispatch') THEN
        json_valid(NEW.prompt_payload)
        AND NOT EXISTS (SELECT 1 FROM json_tree(NEW.prompt_payload) WHERE type = 'null')
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.wakeup_payload_kind') = 'text'
        AND json_extract(NEW.prompt_payload, '$.wakeup_payload_kind') = 'dag_downstream'
        AND json_type(NEW.prompt_payload, '$.schema_version') = 'integer'
        AND json_extract(NEW.prompt_payload, '$.schema_version') = 1
        AND json_type(NEW.prompt_payload, '$.agent_id') = 'text'
        AND length(trim(json_extract(NEW.prompt_payload, '$.agent_id'))) > 0
        AND json_extract(NEW.prompt_payload, '$.agent_id') = trim(json_extract(NEW.prompt_payload, '$.agent_id'))
        AND length(trim(COALESCE(NEW.target_agent_id, ''))) > 0
        AND NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(json_extract(NEW.prompt_payload, '$.agent_id')) = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    WHEN NEW.wakeup_kind = 'start' THEN
        json_valid(NEW.prompt_payload)
        AND NOT EXISTS (SELECT 1 FROM json_tree(NEW.prompt_payload) WHERE type = 'null')
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.wakeup_payload_kind') = 'text'
        AND json_extract(NEW.prompt_payload, '$.wakeup_payload_kind') = 'legacy_launch'
        AND json_type(NEW.prompt_payload, '$.schema_version') = 'integer'
        AND json_extract(NEW.prompt_payload, '$.schema_version') = 1
        AND json_type(NEW.prompt_payload, '$.agent_id') = 'text'
        AND length(trim(json_extract(NEW.prompt_payload, '$.agent_id'))) > 0
        AND json_extract(NEW.prompt_payload, '$.agent_id') = trim(json_extract(NEW.prompt_payload, '$.agent_id'))
        AND length(trim(COALESCE(NEW.target_agent_id, ''))) > 0
        AND NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(json_extract(NEW.prompt_payload, '$.agent_id')) = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.dag_key, '')) = ''
        AND trim(COALESCE(NEW.node_key, '')) = ''
        AND NEW.run_id IS NULL
    WHEN NEW.wakeup_kind = 'turn_complete_retry' THEN
        NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.target_agent_id, '')) = 'mcp-orch'
        AND json_valid(NEW.prompt_payload)
        AND json_type(NEW.prompt_payload) = 'object'
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    WHEN NEW.wakeup_kind = 'terminal_failure_compensation' THEN
        NEW.target_agent_id = trim(NEW.target_agent_id)
        AND trim(COALESCE(NEW.target_agent_id, '')) = 'mcp-orch'
        AND json_valid(NEW.prompt_payload)
        AND json_type(NEW.prompt_payload) = 'object'
        AND json_type(NEW.prompt_payload, '$.reason') = 'text'
        AND json_type(NEW.prompt_payload, '$.store_error') = 'text'
        AND (json_type(NEW.prompt_payload, '$.fail_fast') = 'true'
            OR json_type(NEW.prompt_payload, '$.fail_fast') = 'false')
        AND trim(COALESCE(NEW.dag_key, '')) <> ''
        AND trim(COALESCE(NEW.node_key, '')) <> ''
        AND COALESCE(NEW.run_id > 0, 0) = 1
        AND EXISTS (SELECT 1 FROM task_dag_runs r
                    WHERE r.id = NEW.run_id AND r.dag_key = NEW.dag_key)
    ELSE 0
 END), 0) = 0
BEGIN
    SELECT RAISE(ABORT, 'task dag wakeup v1 envelope is invalid');
END;

-- Versioned rows are canonical records. Their kind, target, payload and DAG
-- identity are immutable; only an explicit 0 -> 1 migration may change them.
CREATE TRIGGER trg_task_dag_wakeups_v134_version_immutable
BEFORE UPDATE OF id, payload_schema_version, dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key ON task_dag_wakeups
WHEN OLD.payload_schema_version = 1
 AND (
    NEW.id IS NOT OLD.id
    OR NEW.payload_schema_version IS NOT OLD.payload_schema_version
    OR NEW.dag_key IS NOT OLD.dag_key
    OR NEW.node_key IS NOT OLD.node_key
    OR NEW.run_id IS NOT OLD.run_id
    OR NEW.wakeup_kind IS NOT OLD.wakeup_kind
    OR NEW.target_agent_id IS NOT OLD.target_agent_id
    OR NEW.prompt_payload IS NOT OLD.prompt_payload
    OR NEW.idempotency_key IS NOT OLD.idempotency_key
 )
BEGIN
    SELECT RAISE(ABORT, 'task dag wakeup payload schema version is immutable');
END;

-- Legacy rows remain byte-for-byte historical records until the explicit
-- migration statement changes payload_schema_version to 1. They cannot be
-- rewritten while still carrying version 0.
CREATE TRIGGER trg_task_dag_wakeups_v134_legacy_immutable
BEFORE UPDATE OF id, dag_key, node_key, run_id, wakeup_kind, target_agent_id, prompt_payload, idempotency_key ON task_dag_wakeups
WHEN OLD.payload_schema_version = 0
 AND NEW.payload_schema_version = 0
 AND (
    NEW.id IS NOT OLD.id
    OR NEW.dag_key IS NOT OLD.dag_key
    OR NEW.node_key IS NOT OLD.node_key
    OR NEW.run_id IS NOT OLD.run_id
    OR NEW.wakeup_kind IS NOT OLD.wakeup_kind
    OR NEW.target_agent_id IS NOT OLD.target_agent_id
    OR NEW.prompt_payload IS NOT OLD.prompt_payload
    OR NEW.idempotency_key IS NOT OLD.idempotency_key
 )
BEGIN
    SELECT RAISE(ABORT, 'legacy task dag wakeup payload is immutable until explicit migration');
END;
