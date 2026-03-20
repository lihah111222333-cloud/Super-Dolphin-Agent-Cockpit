-- 0012_agent_threads.sql — Codex Agent 线程注册表。
--
-- 用途: agent 启动后注册 (port, pid, status), 支持按端口/PID 服务发现。
-- Go 代码: internal/store/agent_thread.go

CREATE TABLE IF NOT EXISTS agent_threads (
    thread_id       TEXT        PRIMARY KEY,
    prompt          TEXT        NOT NULL DEFAULT '',
    model           TEXT        NOT NULL DEFAULT '',
    cwd             TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL DEFAULT 'running',
    port            INT         NOT NULL DEFAULT 0,
    pid             INT         NOT NULL DEFAULT 0,
    created_at      BIGINT      NOT NULL DEFAULT 0,
    updated_at      BIGINT      NOT NULL DEFAULT 0,
    finished_at     BIGINT,
    last_event_type TEXT        NOT NULL DEFAULT '',
    error_message   TEXT        NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_agent_threads_status
    ON agent_threads(status);

CREATE INDEX IF NOT EXISTS idx_agent_threads_port
    ON agent_threads(port);

CREATE INDEX IF NOT EXISTS idx_agent_threads_pid
    ON agent_threads(pid);
