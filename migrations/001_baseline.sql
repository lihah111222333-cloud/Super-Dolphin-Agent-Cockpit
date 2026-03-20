-- V3 baseline schema, extracted from V2 pg_schema_public.sql

CREATE FUNCTION public.prevent_agent_codex_binding_rebind() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        RAISE EXCEPTION 'agent_codex_binding.agent_id is immutable';
    END IF;
    IF NEW.codex_thread_id IS DISTINCT FROM OLD.codex_thread_id THEN
        RAISE EXCEPTION 'agent_codex_binding.codex_thread_id is immutable for agent_id=%', OLD.agent_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION public.prevent_agent_provider_binding_rebind() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.agent_id IS DISTINCT FROM OLD.agent_id THEN
        RAISE EXCEPTION 'agent_provider_binding.agent_id is immutable';
    END IF;
    IF NEW.provider IS DISTINCT FROM OLD.provider THEN
        RAISE EXCEPTION 'agent_provider_binding.provider is immutable for agent_id=%', OLD.agent_id;
    END IF;
    IF NEW.provider_thread_id IS DISTINCT FROM OLD.provider_thread_id THEN
        RAISE EXCEPTION 'agent_provider_binding.provider_thread_id is immutable for agent_id=%', OLD.agent_id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TABLE public.agent_codex_binding (
    agent_id text NOT NULL, codex_thread_id text NOT NULL, rollout_path text DEFAULT ''::text NOT NULL,
    created_at bigint DEFAULT 0 NOT NULL, updated_at bigint DEFAULT 0 NOT NULL,
    cwd text DEFAULT ''::text NOT NULL, archived boolean DEFAULT false NOT NULL
);
CREATE TABLE public.agent_interactions (
    id bigint NOT NULL, thread_id text DEFAULT ''::text NOT NULL, parent_id bigint, sender text NOT NULL,
    receiver text DEFAULT ''::text NOT NULL, msg_type text DEFAULT 'task'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL, requires_review boolean DEFAULT false NOT NULL,
    reviewed_by text DEFAULT ''::text NOT NULL, review_note text DEFAULT ''::text NOT NULL,
    reviewed_at timestamp with time zone, payload jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.agent_provider_binding (
    agent_id text NOT NULL, provider text NOT NULL, provider_thread_id text NOT NULL,
    codex_thread_id text DEFAULT ''::text NOT NULL, rollout_path text DEFAULT ''::text NOT NULL,
    cwd text DEFAULT ''::text NOT NULL, archived boolean DEFAULT false NOT NULL,
    created_at bigint DEFAULT 0 NOT NULL, updated_at bigint DEFAULT 0 NOT NULL
);
CREATE TABLE public.agent_status (
    agent_id text NOT NULL, agent_name text DEFAULT ''::text NOT NULL,
    session_id text DEFAULT ''::text NOT NULL, status text DEFAULT 'unknown'::text NOT NULL,
    stagnant_sec integer DEFAULT 0 NOT NULL, error text DEFAULT ''::text NOT NULL,
    output_tail jsonb DEFAULT '[]'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.agent_threads (
    thread_id text NOT NULL, prompt text DEFAULT ''::text NOT NULL, model text DEFAULT ''::text NOT NULL,
    cwd text DEFAULT ''::text NOT NULL, status text DEFAULT 'running'::text NOT NULL,
    port integer DEFAULT 0 NOT NULL, pid integer DEFAULT 0 NOT NULL,
    created_at bigint DEFAULT 0 NOT NULL, updated_at bigint DEFAULT 0 NOT NULL,
    finished_at bigint, last_event_type text DEFAULT ''::text NOT NULL,
    error_message text DEFAULT ''::text NOT NULL, workspace_run_key text DEFAULT ''::text NOT NULL,
    owner_thread_id text DEFAULT ''::text NOT NULL
);
CREATE TABLE public.audit_events (
    id bigint NOT NULL, ts timestamp with time zone DEFAULT now() NOT NULL,
    event_type text NOT NULL, action text NOT NULL, result text NOT NULL,
    actor text DEFAULT ''::text NOT NULL, target text DEFAULT ''::text NOT NULL,
    detail text DEFAULT ''::text NOT NULL, level text DEFAULT 'INFO'::text NOT NULL, extra jsonb
);
CREATE TABLE public.bus_exception_logs (
    id bigint NOT NULL, ts timestamp with time zone DEFAULT now() NOT NULL,
    category text DEFAULT 'unknown'::text NOT NULL, severity text DEFAULT 'error'::text NOT NULL,
    source text DEFAULT ''::text NOT NULL, tool_name text DEFAULT ''::text NOT NULL,
    message text DEFAULT ''::text NOT NULL, traceback text DEFAULT ''::text NOT NULL, extra jsonb
);
CREATE TABLE public.command_card_runs (
    id bigint NOT NULL, card_key text NOT NULL, requested_by text DEFAULT ''::text NOT NULL,
    params jsonb DEFAULT '{}'::jsonb NOT NULL, rendered_command text NOT NULL,
    risk_level text DEFAULT 'normal'::text NOT NULL, status text DEFAULT 'pending_review'::text NOT NULL,
    requires_review boolean DEFAULT true NOT NULL, interaction_id bigint,
    output text DEFAULT ''::text NOT NULL, error text DEFAULT ''::text NOT NULL,
    exit_code integer, created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL, executed_at timestamp with time zone
);
CREATE TABLE public.command_card_versions (
    id bigint NOT NULL, card_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL, command_template text NOT NULL, args_schema jsonb,
    risk_level text DEFAULT 'normal'::text NOT NULL, enabled boolean DEFAULT true NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    source_updated_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.command_cards (
    id bigint NOT NULL, card_key text NOT NULL, title text NOT NULL,
    description text DEFAULT ''::text NOT NULL, command_template text NOT NULL, args_schema jsonb,
    risk_level text DEFAULT 'normal'::text NOT NULL, enabled boolean DEFAULT true NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.cwd_instance_locks (
    cwd text NOT NULL, instance_id text NOT NULL, pid integer DEFAULT 0 NOT NULL,
    acquired_at timestamp with time zone DEFAULT now() NOT NULL,
    heartbeat_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.prompt_template_versions (
    id bigint NOT NULL, prompt_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    agent_key text DEFAULT ''::text NOT NULL, tool_name text DEFAULT ''::text NOT NULL,
    prompt_text text NOT NULL, variables jsonb, tags jsonb, enabled boolean DEFAULT true NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    source_updated_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.prompt_versions (
    id bigint NOT NULL, prompt_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    agent_key text DEFAULT ''::text NOT NULL, tool_name text DEFAULT ''::text NOT NULL,
    prompt_text text NOT NULL, variables jsonb, tags jsonb, enabled boolean DEFAULT true NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    source_updated_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.prompt_templates (
    id bigint NOT NULL, prompt_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    agent_key text DEFAULT ''::text NOT NULL, tool_name text DEFAULT ''::text NOT NULL,
    prompt_text text NOT NULL, variables jsonb, tags jsonb, enabled boolean DEFAULT true NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    description text DEFAULT ''::text NOT NULL
);
CREATE TABLE public.prompts (
    id bigint NOT NULL, agent_key text NOT NULL, tool_name text NOT NULL,
    prompt_text text DEFAULT ''::text NOT NULL, is_pinned boolean DEFAULT false NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.schema_migrations (
    version integer NOT NULL, name text NOT NULL, filename text NOT NULL,
    applied_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.shared_files (
    path text NOT NULL, content text NOT NULL, updated_by text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.system_logs (
    id bigint NOT NULL, ts timestamp with time zone DEFAULT now() NOT NULL,
    level text NOT NULL, logger text NOT NULL, message text NOT NULL,
    raw text DEFAULT ''::text NOT NULL, source text DEFAULT ''::text NOT NULL,
    component text DEFAULT ''::text NOT NULL, agent_id text DEFAULT ''::text NOT NULL,
    thread_id text DEFAULT ''::text NOT NULL, trace_id text DEFAULT ''::text NOT NULL,
    event_type text DEFAULT ''::text NOT NULL, tool_name text DEFAULT ''::text NOT NULL,
    duration_ms integer, extra jsonb
);
CREATE TABLE public.task_acks (
    id bigint NOT NULL, ack_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL, assigned_to text DEFAULT ''::text NOT NULL,
    requested_by text DEFAULT ''::text NOT NULL, priority text DEFAULT 'normal'::text NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL, progress integer DEFAULT 0 NOT NULL,
    ack_message text DEFAULT ''::text NOT NULL, result_summary text DEFAULT ''::text NOT NULL,
    metadata jsonb, due_at timestamp with time zone, acked_at timestamp with time zone,
    started_at timestamp with time zone, finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.task_dag_nodes (
    id bigint NOT NULL, dag_key text NOT NULL, node_key text NOT NULL,
    title text DEFAULT ''::text NOT NULL, node_type text DEFAULT 'task'::text NOT NULL,
    assigned_to text DEFAULT ''::text NOT NULL, depends_on jsonb DEFAULT '[]'::jsonb NOT NULL,
    status text DEFAULT 'pending'::text NOT NULL, command_ref text DEFAULT ''::text NOT NULL,
    config jsonb, result jsonb, started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.task_dags (
    id bigint NOT NULL, dag_key text NOT NULL, title text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL, status text DEFAULT 'draft'::text NOT NULL,
    created_by text DEFAULT ''::text NOT NULL, metadata jsonb, started_at timestamp with time zone,
    finished_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.task_traces (
    id bigint NOT NULL, trace_id text NOT NULL, span_id text NOT NULL,
    parent_span_id text DEFAULT ''::text NOT NULL, span_name text NOT NULL,
    component text NOT NULL, status text DEFAULT 'running'::text NOT NULL,
    input_payload jsonb, output_payload jsonb, error_text text DEFAULT ''::text NOT NULL,
    metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    started_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone, duration_ms integer DEFAULT 0 NOT NULL
);
CREATE TABLE public.topology_approval_archives (
    id text NOT NULL, status text NOT NULL, requested_by text DEFAULT ''::text NOT NULL,
    reason text DEFAULT ''::text NOT NULL, created_at timestamp with time zone NOT NULL,
    expire_at timestamp with time zone NOT NULL, reviewed_at timestamp with time zone,
    reviewer text DEFAULT ''::text NOT NULL, review_note text DEFAULT ''::text NOT NULL,
    arch_hash text NOT NULL, proposed_architecture jsonb NOT NULL,
    archived_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.topology_approvals (
    id text NOT NULL, status text NOT NULL, requested_by text DEFAULT ''::text NOT NULL,
    reason text DEFAULT ''::text NOT NULL, created_at timestamp with time zone NOT NULL,
    expire_at timestamp with time zone NOT NULL, reviewed_at timestamp with time zone,
    reviewer text DEFAULT ''::text NOT NULL, review_note text DEFAULT ''::text NOT NULL,
    arch_hash text NOT NULL, proposed_architecture jsonb NOT NULL
);
CREATE TABLE public.ui_preferences (
    key text NOT NULL, value jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    cwd text DEFAULT ''::text NOT NULL
);
CREATE TABLE public.workspace_run_files (
    id bigint NOT NULL, run_key text NOT NULL, relative_path text NOT NULL,
    baseline_sha256 text DEFAULT ''::text NOT NULL, workspace_sha256 text DEFAULT ''::text NOT NULL,
    source_sha256_before text DEFAULT ''::text NOT NULL,
    source_sha256_after text DEFAULT ''::text NOT NULL,
    state text DEFAULT 'tracked'::text NOT NULL, last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
CREATE TABLE public.workspace_runs (
    id bigint NOT NULL, run_key text NOT NULL, dag_key text DEFAULT ''::text NOT NULL,
    source_root text NOT NULL, workspace_path text NOT NULL,
    status text DEFAULT 'active'::text NOT NULL, created_by text DEFAULT ''::text NOT NULL,
    updated_by text DEFAULT ''::text NOT NULL, metadata jsonb DEFAULT '{}'::jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    finished_at timestamp with time zone
);

CREATE INDEX idx_acb_codex_thread ON public.agent_codex_binding USING btree (codex_thread_id);
CREATE INDEX idx_acb_created_at_desc ON public.agent_codex_binding USING btree (created_at DESC);
CREATE INDEX idx_acb_cwd ON public.agent_codex_binding USING btree (cwd);
CREATE INDEX idx_agent_interactions_sender_receiver ON public.agent_interactions USING btree (sender, receiver);
CREATE INDEX idx_agent_interactions_status_review ON public.agent_interactions USING btree (status, requires_review, created_at DESC);
CREATE INDEX idx_agent_interactions_thread_created ON public.agent_interactions USING btree (thread_id, created_at DESC);
CREATE INDEX idx_agent_status_status_updated ON public.agent_status USING btree (status, updated_at DESC);
CREATE INDEX idx_agent_status_status_updated_at ON public.agent_status USING btree (status, updated_at DESC);
CREATE INDEX idx_agent_status_updated_at ON public.agent_status USING btree (updated_at DESC);
CREATE INDEX idx_agent_status_updated_at_desc ON public.agent_status USING btree (updated_at DESC);
CREATE INDEX idx_agent_threads_owner_thread_id ON public.agent_threads USING btree (owner_thread_id);
CREATE INDEX idx_agent_threads_pid ON public.agent_threads USING btree (pid);
CREATE INDEX idx_agent_threads_port ON public.agent_threads USING btree (port);
CREATE INDEX idx_agent_threads_status ON public.agent_threads USING btree (status);
CREATE INDEX idx_agent_threads_workspace_run_key ON public.agent_threads USING btree (workspace_run_key);
CREATE INDEX idx_audit_events_action ON public.audit_events USING btree (action);
CREATE INDEX idx_audit_events_actor ON public.audit_events USING btree (actor);
CREATE INDEX idx_audit_events_event_type ON public.audit_events USING btree (event_type);
CREATE INDEX idx_audit_events_result ON public.audit_events USING btree (result);
CREATE INDEX idx_audit_events_ts ON public.audit_events USING btree (ts DESC);
CREATE INDEX idx_bus_exception_logs_category ON public.bus_exception_logs USING btree (category);
CREATE INDEX idx_bus_exception_logs_severity ON public.bus_exception_logs USING btree (severity);
CREATE INDEX idx_bus_exception_logs_ts ON public.bus_exception_logs USING btree (ts DESC);
CREATE INDEX idx_command_card_runs_card_key ON public.command_card_runs USING btree (card_key, created_at DESC);
CREATE INDEX idx_command_card_runs_status_created ON public.command_card_runs USING btree (status, created_at DESC);
CREATE INDEX idx_command_card_versions_key_id ON public.command_card_versions USING btree (card_key, id DESC);
CREATE INDEX idx_command_cards_risk_enabled ON public.command_cards USING btree (risk_level, enabled, updated_at DESC);
CREATE INDEX idx_cwd_instance_locks_heartbeat ON public.cwd_instance_locks USING btree (heartbeat_at);
CREATE INDEX idx_prompt_templates_agent_tool ON public.prompt_templates USING btree (agent_key, tool_name);
CREATE INDEX idx_prompt_templates_enabled ON public.prompt_templates USING btree (enabled, updated_at DESC);
CREATE INDEX idx_prompt_versions_key_id ON public.prompt_versions USING btree (prompt_key, id DESC);
CREATE INDEX idx_prompts_agent_key ON public.prompts USING btree (agent_key);
CREATE INDEX idx_prompts_sort_order ON public.prompts USING btree (sort_order, agent_key);
CREATE INDEX idx_shared_files_updated_at ON public.shared_files USING btree (updated_at DESC);
CREATE INDEX idx_system_logs_agent ON public.system_logs USING btree (agent_id) WHERE (agent_id <> ''::text);
CREATE INDEX idx_system_logs_event ON public.system_logs USING btree (event_type) WHERE (event_type <> ''::text);
CREATE INDEX idx_system_logs_level ON public.system_logs USING btree (level);
CREATE INDEX idx_system_logs_logger ON public.system_logs USING btree (logger);
CREATE INDEX idx_system_logs_source ON public.system_logs USING btree (source) WHERE (source <> ''::text);
CREATE INDEX idx_system_logs_thread ON public.system_logs USING btree (thread_id) WHERE (thread_id <> ''::text);
CREATE INDEX idx_system_logs_tool ON public.system_logs USING btree (tool_name) WHERE (tool_name <> ''::text);
CREATE INDEX idx_system_logs_ts ON public.system_logs USING btree (ts DESC);
CREATE INDEX idx_task_acks_assigned_to ON public.task_acks USING btree (assigned_to);
CREATE INDEX idx_task_acks_due_at ON public.task_acks USING btree (due_at);
CREATE INDEX idx_task_acks_priority ON public.task_acks USING btree (priority, status);
CREATE INDEX idx_task_acks_status ON public.task_acks USING btree (status, updated_at DESC);
CREATE INDEX idx_task_dag_nodes_dag_key ON public.task_dag_nodes USING btree (dag_key, id);
CREATE INDEX idx_task_dag_nodes_status ON public.task_dag_nodes USING btree (status);
CREATE INDEX idx_task_dags_status ON public.task_dags USING btree (status, updated_at DESC);
CREATE INDEX idx_task_traces_component_started ON public.task_traces USING btree (component, started_at DESC);
CREATE INDEX idx_task_traces_trace_started ON public.task_traces USING btree (trace_id, started_at, id);
CREATE INDEX idx_topology_approval_archives_archived_at ON public.topology_approval_archives USING btree (archived_at DESC);
CREATE INDEX idx_topology_approvals_arch_hash ON public.topology_approvals USING btree (arch_hash);
CREATE INDEX idx_topology_approvals_status_created_at ON public.topology_approvals USING btree (status, created_at DESC);
CREATE INDEX idx_ui_preferences_key ON public.ui_preferences USING btree (key);
CREATE INDEX idx_workspace_run_files_run_path ON public.workspace_run_files USING btree (run_key, relative_path);
CREATE INDEX idx_workspace_run_files_run_state ON public.workspace_run_files USING btree (run_key, state, updated_at DESC);
CREATE INDEX idx_workspace_runs_dag ON public.workspace_runs USING btree (dag_key, updated_at DESC);
CREATE INDEX idx_workspace_runs_status_updated ON public.workspace_runs USING btree (status, updated_at DESC);

CREATE TRIGGER trg_prevent_agent_codex_binding_rebind BEFORE UPDATE ON public.agent_codex_binding FOR EACH ROW EXECUTE FUNCTION public.prevent_agent_codex_binding_rebind();
CREATE TRIGGER trg_prevent_agent_provider_binding_rebind BEFORE UPDATE ON public.agent_provider_binding FOR EACH ROW EXECUTE FUNCTION public.prevent_agent_provider_binding_rebind();
