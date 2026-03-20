-- ACK 管理 & DAG 管理表

-- ── task_acks: 任务确认/进度跟踪 ──

CREATE TABLE IF NOT EXISTS task_acks (
    id          BIGSERIAL    PRIMARY KEY,
    ack_key     TEXT         NOT NULL UNIQUE,
    title       TEXT         NOT NULL DEFAULT '',
    description TEXT         NOT NULL DEFAULT '',
    assigned_to TEXT         NOT NULL DEFAULT '',
    requested_by TEXT        NOT NULL DEFAULT '',
    priority    TEXT         NOT NULL DEFAULT 'normal',
    status      TEXT         NOT NULL DEFAULT 'pending',
    progress    INT          NOT NULL DEFAULT 0,
    ack_message TEXT         NOT NULL DEFAULT '',
    result_summary TEXT      NOT NULL DEFAULT '',
    metadata    JSONB,
    due_at      TIMESTAMPTZ,
    acked_at    TIMESTAMPTZ,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_acks_status      ON task_acks (status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_task_acks_priority     ON task_acks (priority, status);
CREATE INDEX IF NOT EXISTS idx_task_acks_assigned_to  ON task_acks (assigned_to);
CREATE INDEX IF NOT EXISTS idx_task_acks_due_at       ON task_acks (due_at);

-- ── task_dags: DAG 主表 ──

CREATE TABLE IF NOT EXISTS task_dags (
    id          BIGSERIAL    PRIMARY KEY,
    dag_key     TEXT         NOT NULL UNIQUE,
    title       TEXT         NOT NULL DEFAULT '',
    description TEXT         NOT NULL DEFAULT '',
    status      TEXT         NOT NULL DEFAULT 'draft',
    created_by  TEXT         NOT NULL DEFAULT '',
    metadata    JSONB,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_dags_status ON task_dags (status, updated_at DESC);

-- ── task_dag_nodes: DAG 节点 ──

CREATE TABLE IF NOT EXISTS task_dag_nodes (
    id          BIGSERIAL    PRIMARY KEY,
    dag_key     TEXT         NOT NULL,
    node_key    TEXT         NOT NULL,
    title       TEXT         NOT NULL DEFAULT '',
    node_type   TEXT         NOT NULL DEFAULT 'task',
    assigned_to TEXT         NOT NULL DEFAULT '',
    depends_on  JSONB        NOT NULL DEFAULT '[]'::jsonb,
    status      TEXT         NOT NULL DEFAULT 'pending',
    command_ref TEXT         NOT NULL DEFAULT '',
    config      JSONB,
    result      JSONB,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (dag_key, node_key)
);

CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_dag_key ON task_dag_nodes (dag_key, id);
CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_status  ON task_dag_nodes (status);
