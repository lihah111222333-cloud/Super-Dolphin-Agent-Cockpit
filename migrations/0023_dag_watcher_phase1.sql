ALTER TABLE task_dag_nodes
  ADD COLUMN IF NOT EXISTS active_turn_id TEXT,
  ADD COLUMN IF NOT EXISTS active_wakeup_id BIGINT,
  ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_task_dag_nodes_assigned_status
ON task_dag_nodes (assigned_to, status);

CREATE TABLE IF NOT EXISTS task_dag_wakeups (
  id               BIGSERIAL PRIMARY KEY,
  dag_key          TEXT NOT NULL,
  node_key         TEXT NOT NULL,
  wakeup_kind      TEXT NOT NULL,
  target_agent_id  TEXT NOT NULL,
  prompt_payload   JSONB NOT NULL,
  idempotency_key  TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'pending',
  attempt_count    INT NOT NULL DEFAULT 0,
  next_retry_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  claimed_at       TIMESTAMPTZ,
  claimed_by       TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  sent_at          TIMESTAMPTZ,
  bound_turn_id    TEXT,
  turn_bound_at    TIMESTAMPTZ,
  last_error       TEXT NOT NULL DEFAULT '',
  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (idempotency_key)
);

ALTER TABLE task_dag_wakeups
  ADD COLUMN IF NOT EXISTS claimed_by TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_task_dag_wakeups_poll
ON task_dag_wakeups (status, next_retry_at, id);

CREATE TABLE IF NOT EXISTS task_dag_worker_leases (
  target_agent_id  TEXT PRIMARY KEY,
  owner_id         TEXT NOT NULL,
  lease_expires_at TIMESTAMPTZ NOT NULL,
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
