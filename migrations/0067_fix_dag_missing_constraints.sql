-- Fix: 001_baseline.sql 导出时丢失了 task_dags / task_dag_nodes 的主键、唯一约束
-- 和自增序列，导致 sqlc 的 ON CONFLICT 和 INSERT 全部报错。
-- 原始定义见 0004_ack_dag.sql (L34-35, L52-53, L67)。
--
-- 注意：本迁移必须幂等。早期 DB（pre-baseline）通过 0004_ack_dag.sql 创建表，
-- PK / UNIQUE 已存在；只有 baseline 导出后建的库才需要补。所以下面所有
-- ADD CONSTRAINT 都用 DO 块 + pg_constraint 检查包起来。

-- ── task_dags ──

-- 补回自增序列（baseline 用 bigint NOT NULL 而非 BIGSERIAL）
CREATE SEQUENCE IF NOT EXISTS task_dags_id_seq AS bigint OWNED BY task_dags.id;
SELECT setval('task_dags_id_seq', COALESCE((SELECT MAX(id) FROM task_dags), 0) + 1, false);
ALTER TABLE task_dags ALTER COLUMN id SET DEFAULT nextval('task_dags_id_seq');

-- 补回主键 + dag_key 唯一（仅当不存在时）
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'public.task_dags'::regclass AND contype = 'p'
  ) THEN
    ALTER TABLE task_dags ADD CONSTRAINT task_dags_pkey PRIMARY KEY (id);
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint c
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
    WHERE c.conrelid = 'public.task_dags'::regclass
      AND c.contype = 'u'
      AND a.attname = 'dag_key'
      AND array_length(c.conkey, 1) = 1
  ) THEN
    ALTER TABLE task_dags ADD CONSTRAINT uq_task_dags_dag_key UNIQUE (dag_key);
  END IF;
END $$;

-- ── task_dag_nodes ──

CREATE SEQUENCE IF NOT EXISTS task_dag_nodes_id_seq AS bigint OWNED BY task_dag_nodes.id;
SELECT setval('task_dag_nodes_id_seq', COALESCE((SELECT MAX(id) FROM task_dag_nodes), 0) + 1, false);
ALTER TABLE task_dag_nodes ALTER COLUMN id SET DEFAULT nextval('task_dag_nodes_id_seq');

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'public.task_dag_nodes'::regclass AND contype = 'p'
  ) THEN
    ALTER TABLE task_dag_nodes ADD CONSTRAINT task_dag_nodes_pkey PRIMARY KEY (id);
  END IF;

  -- 检查 (dag_key, node_key) 这一对是否已有任意 unique 约束（不论叫什么名字）
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint c
    WHERE c.conrelid = 'public.task_dag_nodes'::regclass
      AND c.contype = 'u'
      AND (
        SELECT array_agg(a.attname ORDER BY a.attname)
        FROM pg_attribute a
        WHERE a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
      ) = ARRAY['dag_key','node_key']::name[]
  ) THEN
    ALTER TABLE task_dag_nodes ADD CONSTRAINT uq_task_dag_nodes_dag_node UNIQUE (dag_key, node_key);
  END IF;
END $$;
