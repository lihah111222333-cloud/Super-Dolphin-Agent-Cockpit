-- 0006_workspace_runs.sql
-- 双通道编排: 虚拟工作区(文件系统) + PG 状态接口

-- ── workspace_runs: 一次编排运行(workspace run)主记录 ──

CREATE TABLE IF NOT EXISTS workspace_runs (
    id             BIGSERIAL    PRIMARY KEY,
    run_key        TEXT         NOT NULL UNIQUE,
    dag_key        TEXT         NOT NULL DEFAULT '',
    source_root    TEXT         NOT NULL,
    workspace_path TEXT         NOT NULL,
    status         TEXT         NOT NULL DEFAULT 'active', -- active|merging|merged|aborted|failed
    created_by     TEXT         NOT NULL DEFAULT '',
    updated_by     TEXT         NOT NULL DEFAULT '',
    metadata       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    finished_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_workspace_runs_status_updated
ON workspace_runs (status, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_workspace_runs_dag
ON workspace_runs (dag_key, updated_at DESC);

-- ── workspace_run_files: run 内文件级追踪/合并状态 ──

CREATE TABLE IF NOT EXISTS workspace_run_files (
    id                       BIGSERIAL    PRIMARY KEY,
    run_key                  TEXT         NOT NULL,
    relative_path            TEXT         NOT NULL,
    baseline_sha256          TEXT         NOT NULL DEFAULT '',
    workspace_sha256         TEXT         NOT NULL DEFAULT '',
    source_sha256_before     TEXT         NOT NULL DEFAULT '',
    source_sha256_after      TEXT         NOT NULL DEFAULT '',
    state                    TEXT         NOT NULL DEFAULT 'tracked', -- tracked|synced|changed|merged|conflict|error|unchanged
    last_error               TEXT         NOT NULL DEFAULT '',
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (run_key, relative_path)
);

CREATE INDEX IF NOT EXISTS idx_workspace_run_files_run_state
ON workspace_run_files (run_key, state, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_workspace_run_files_run_path
ON workspace_run_files (run_key, relative_path);
