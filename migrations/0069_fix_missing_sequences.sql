-- 0069_fix_missing_sequences.sql
--
-- 001_baseline.sql 由 pg_dump 导出时把所有 BIGSERIAL 列降级为 bigint NOT NULL，
-- 丢失了自增序列（以及部分 PK/UNIQUE 约束）。对于从 baseline 初始化的空库，
-- 任何 INSERT 省略 id 的语句都会报 "null value in column id violates not-null
-- constraint"。
--
-- 本迁移为仍然缺少序列的表补回自增 DEFAULT，并补回 task_acks 缺失的 PK/UNIQUE。
-- 所有操作幂等：通过 pg_get_serial_sequence 检测是否已有序列��

-- ────────────────────────────────────────────────────────────────────────────
-- Helper function: 为指定表的 id 列补回 BIGSERIAL 等价物
-- 自动继承表 owner，避免跨用户执行时报权限错误。
-- ────────────────────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION _fix_missing_id_seq(tbl text) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    seq_name text;
    current_max bigint;
    tbl_owner text;
BEGIN
    IF to_regclass('public.' || tbl) IS NULL THEN RETURN; END IF;
    IF pg_get_serial_sequence('public.' || tbl, 'id') IS NOT NULL THEN RETURN; END IF;

    seq_name := tbl || '_id_seq';

    SELECT tableowner INTO tbl_owner
    FROM pg_tables WHERE schemaname = 'public' AND tablename = tbl;

    EXECUTE format('CREATE SEQUENCE IF NOT EXISTS public.%I AS bigint', seq_name);
    EXECUTE format('ALTER SEQUENCE public.%I OWNER TO %I', seq_name, tbl_owner);

    EXECUTE format('SELECT COALESCE(MAX(id), 0) FROM public.%I', tbl) INTO current_max;
    PERFORM setval('public.' || seq_name, GREATEST(current_max, 1), current_max > 0);

    EXECUTE format('ALTER TABLE public.%I ALTER COLUMN id SET DEFAULT nextval(%L)',
                   tbl, 'public.' || seq_name);
    EXECUTE format('ALTER SEQUENCE public.%I OWNED BY public.%I.id', seq_name, tbl);
END $$;

-- ─────────────────────────────────────────────────────────────────────���──────
-- 补回所有缺失的序列
-- (prompt_templates/prompt_versions 已由 0033 修复)
-- (task_dags/task_dag_nodes 已由 0067 修复)
-- ────────────────────────────────────────────────────────────────────────────

SELECT _fix_missing_id_seq('audit_events');
SELECT _fix_missing_id_seq('system_logs');
SELECT _fix_missing_id_seq('command_cards');
SELECT _fix_missing_id_seq('command_card_versions');
SELECT _fix_missing_id_seq('command_card_runs');
SELECT _fix_missing_id_seq('agent_interactions');
SELECT _fix_missing_id_seq('task_traces');
SELECT _fix_missing_id_seq('workspace_runs');
SELECT _fix_missing_id_seq('workspace_run_files');
SELECT _fix_missing_id_seq('task_acks');
SELECT _fix_missing_id_seq('bus_exception_logs');
SELECT _fix_missing_id_seq('prompts');
SELECT _fix_missing_id_seq('prompt_template_versions');

-- 清理 helper function
DROP FUNCTION _fix_missing_id_seq(text);

-- ────────────────────────────────────────────────────────────────────────────
-- 补回 task_acks 缺失的 PK 和 UNIQUE 约束
-- (0030 修了其他表但遗漏了 task_acks)
-- ────────────────────────────────────────────────────────────────────────────

DO $$
BEGIN
    IF to_regclass('public.task_acks') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint
           WHERE conrelid = 'public.task_acks'::regclass AND contype = 'p'
       ) THEN
        ALTER TABLE public.task_acks ADD CONSTRAINT task_acks_pkey PRIMARY KEY (id);
    END IF;
END $$;

DO $$
BEGIN
    IF to_regclass('public.task_acks') IS NOT NULL
       AND NOT EXISTS (
           SELECT 1 FROM pg_constraint c
           JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY(c.conkey)
           WHERE c.conrelid = 'public.task_acks'::regclass
             AND c.contype = 'u'
             AND a.attname = 'ack_key'
             AND array_length(c.conkey, 1) = 1
       ) THEN
        ALTER TABLE public.task_acks ADD CONSTRAINT task_acks_ack_key_key UNIQUE (ack_key);
    END IF;
END $$;
