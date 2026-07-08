package tools

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// defineTaskReadTool 创建只读 task 工具定义，并固定低风险、workflow.read 权限。
// 调用方只能覆盖 audit/capability，不能在单个工具里绕开治理元数据。
func defineTaskReadTool(name, description string, schema Schema, handler ToolHandler, auditEvent string, capabilities ...string) ToolDefinition {
	return defineGovernedTool(name, description, schema, handler, taskToolMetadata(
		ToolRiskLow,
		ToolPermissionWorkflowRead,
		ToolIdempotencyNone,
		auditEvent,
		capabilities...,
	))
}

// defineTaskWriteTool 创建写操作 task 工具定义，并固定高风险、workflow.write 权限。
// 幂等要求由调用方显式传入，避免写工具忘记声明重试边界。
func defineTaskWriteTool(name, description string, schema Schema, handler ToolHandler, auditEvent string, idempotency ToolIdempotencyRequirement, capabilities ...string) ToolDefinition {
	return defineGovernedTool(name, description, schema, handler, taskToolMetadata(
		ToolRiskHigh,
		ToolPermissionWorkflowWrite,
		idempotency,
		auditEvent,
		capabilities...,
	))
}

// taskToolMetadata 构建 task 工具的治理元数据对象。
func taskToolMetadata(risk ToolRiskClass, permission ToolPermission, idempotency ToolIdempotencyRequirement, auditEvent string, capabilities ...string) ToolMetadata {
	if len(capabilities) == 0 {
		capabilities = []string{auditEvent}
	}
	return ToolMetadata{
		Version:                "workflow.task.v1",
		OutputSchema:           RawObjectSchema("Task tool response object."),
		Capabilities:           append([]string(nil), capabilities...),
		RiskClass:              risk,
		Permission:             permission,
		WorkspaceScope:         ToolWorkspaceScopeWorkflow,
		TimeoutSeconds:         120,
		IdempotencyRequirement: idempotency,
		ApprovalRequired:       false,
		AuditEventType:         auditEvent,
		RedactionPolicy:        ToolRedactionMetadataOnly,
	}
}

func applyOpsInputSchema() Schema {
	return ObjectSchema(map[string]Schema{
		"pos":          StringSchema("Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
		"dag_key":      StringSchema("Target DAG key."),
		"base_version": IntegerSchema("Expected current dag.version (OCC; mismatch returns conflict)."),
		"action":       EnumStringSchema("Flat action. Omit or use apply_ops_raw with ops for legacy raw batches.", applyOpsActionEnum...),
		"ops":          ArraySchema(applyOpsOpSchema(), "Typed ops array; each item must include 'op' discriminator."),
		"node_key":     StringSchema("Target node key for add_node/update_node/remove_node."),
		"title":        StringSchema("Flat title for add_node/update_node/update_dag."),
		"description":  StringSchema("Flat DAG description for action=update_dag."),
		"trigger":      StringSchema("Flat DAG trigger for action=update_dag."),
		"cron_expr":    StringSchema("Flat DAG cron expression for action=update_dag."),
		"owner_id":     StringSchema("Flat DAG owner id for action=update_dag."),
		"node_type":    EnumStringSchema("Node type for action=add_node. Hybrid is reserved until runtime support is complete.", creatableNodeTypeEnum...),
		"assigned_to":  StringSchema("Flat node assignee for action=update_node."),
		"depends_on":   ArraySchema(StringSchema("Dependency node key."), "Flat node dependencies for add_node/update_node."),
		"reads":        ArraySchema(StringSchema("Readable artifact or node output reference."), "Flat node reads for add_node/update_node."),
		"writes":       ArraySchema(StringSchema("Writable artifact or node output reference."), "Flat node writes for add_node/update_node."),
		"config":       RawObjectSchema("Flat node config for add_node/update_node."),
		"patch":        RawObjectSchema("Advanced raw patch object for update_dag/update_node."),
	}, "base_version")
}

// taskToolDefinitions 定义 workflow DAG 工具及其治理元数据。
func taskToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTaskWriteTool("task_create_dag", "Create a new DAG template and its nodes in the orchestration store. Existing dag_key values are not replaced; scheduled triggers must be enabled later via task_dag_apply_ops with base_version and cron_expr. This does not start execution; if the user asked to run/execute now, call task_start_dag after create succeeds.", createDAGSchema(), HandleCreateDAG(svc), "workflow.dag.create", ToolIdempotencyRequired, "workflow.definition.write"),
		defineTaskWriteTool("task_dag_apply_ops", "Apply a typed ops batch or one flat action (add_node / update_node / remove_node / update_dag) with base_version OCC. Use action+flat fields for common edits; use ops for advanced raw batches.", applyOpsInputSchema(), HandleApplyOps(svc), "workflow.dag.apply_ops", ToolIdempotencyRequired, "workflow.definition.write"),
		defineTaskWriteTool("task_update_node", "Update the runtime status for a DAG node.", ObjectSchema(map[string]Schema{
			"pos":      StringSchema("Flattened runtime-node locator, e.g. dag:<dag_key>/run_id:<run_id>/node:<node_key>. Preferred over legacy dag_key/node_key/run_id."),
			"dag_key":  StringSchema("DAG key."),
			"node_key": StringSchema("Node key within the DAG."),
			"run_id":   IntegerSchema("Task DAG run id that owns the runtime node."),
			"status":   EnumStringSchema("New node status.", updateNodeStatusEnum...),
			"result":   StringSchema("Optional result summary."),
		}, "status"), HandleUpdateNode(svc), "workflow.node.update", ToolIdempotencyRecommended, "workflow.runtime.write"),
		defineTaskWriteTool("task_dispatch_node", "Explicitly assign an agent to a pending/ready DAG node and enqueue a wakeup so the dispatcher launches it. Use when a node has assigned_to='' (ADR-004 Open Q1).", ObjectSchema(map[string]Schema{
			"pos":         StringSchema("Flattened runtime-node locator, e.g. dag:<dag_key>/run_id:<run_id>/node:<node_key>. Preferred over legacy dag_key/node_key/run_id."),
			"dag_key":     StringSchema("DAG key."),
			"node_key":    StringSchema("Node key within the DAG."),
			"run_id":      IntegerSchema("Task DAG run id that owns the runtime node."),
			"assigned_to": StringSchema("Agent id to dispatch the node to."),
		}, "assigned_to"), HandleDispatchNode(svc), "workflow.node.dispatch", ToolIdempotencyRecommended, "workflow.runtime.write"),
		defineTaskWriteTool("task_start_dag", "Trigger a DAG execution (creates a run and snapshots dag.version). The response includes run_id, scheduled_wakeups, and execution_state; if scheduled_wakeups=0 with waiting_for_assignee, call task_dispatch_node.", ObjectSchema(map[string]Schema{
			"pos":             StringSchema("Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
			"dag_key":         StringSchema("DAG to start."),
			"trigger_source":  EnumStringSchema("Trigger source.", startDAGTriggerEnum...),
			"idempotency_key": StringSchema("Optional, prevents duplicate run on retry."),
		}), HandleStartDAG(svc), "workflow.run.start", ToolIdempotencyRecommended, "workflow.runtime.write"),
		defineTaskWriteTool("task_terminate_dag", "Cancel one running DAG execution by run_key. This marks non-terminal runtime nodes cancelled and stops pending/dispatching/sent wakeups.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened DAG run locator, e.g. dag:<dag_key>/run:<run_key>. Preferred over legacy dag_key/run_key."),
			"dag_key": StringSchema("DAG key used as a fence for the run."),
			"run_key": StringSchema("Run key to cancel."),
			"reason":  StringSchema("Optional cancellation reason."),
		}), HandleTerminateDAG(svc), "workflow.run.terminate", ToolIdempotencyRecommended, "workflow.runtime.write"),
		defineTaskWriteTool("task_delete_dag", "Delete a DAG template and its completed run history. Refuses deletion while any run is active.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
			"dag_key": StringSchema("DAG key to delete."),
		}), HandleDeleteDAG(svc), "workflow.dag.delete", ToolIdempotencyRequired, "workflow.definition.write"),
		defineTaskReadTool("task_list_dags", "List DAGs for console views and final_output retention checks.", ObjectSchema(map[string]Schema{
			"status":  EnumStringSchema("Optional status filter.", listDAGsStatusEnum...),
			"keyword": StringSchema("Optional keyword filter."),
			"limit":   IntegerSchema("Optional max rows; defaults to service limit when 0/omitted."),
		}), HandleListDAGs(svc), "workflow.dag.list", "workflow.definition.read"),
		defineTaskReadTool("task_get_dag", "Fetch a DAG and all of its nodes.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
			"dag_key": StringSchema("Unique DAG key."),
		}), HandleGetDAG(svc), "workflow.dag.get", "workflow.definition.read"),
		defineTaskReadTool("task_get_run", "Fetch a single DAG run by run_key, including the run's runtime node snapshot. task_get_dag reads the DAG template.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened run locator, e.g. run:<run_key> or dag:<dag_key>/run:<run_key>. Preferred over legacy run_key."),
			"run_key": StringSchema("Run key returned by task_start_dag."),
		}), HandleGetRun(svc), "workflow.run.get", "workflow.runtime.read"),
		defineTaskReadTool("task_list_runs", "List recent runs for a DAG (object response wraps the runs slice for forward-compatibility). Status enum mirrors migration 0080 task_dag_runs.status CHECK.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."),
			"dag_key": StringSchema("DAG key to list runs for."),
			"status":  EnumStringSchema("Optional status filter.", listRunsStatusEnum...),
			"limit":   IntegerSchema("Optional max rows; defaults to 50 when 0/omitted."),
		}), HandleListRuns(svc), "workflow.run.list", "workflow.runtime.read"),
		workflowDiagnosticsToolDefinition(svc),
		workflowRecoveryActionToolDefinition(svc),
		defineTaskReadTool("task_diagnose_dag_prompt_identity_gaps", "Read-only diagnostic for historical DAG nodes missing prompt_key/agent_key or hybrid verifier provider/Codex identity. It never rewrites DAGs; use task_dag_apply_ops for explicit rebind or recreate the DAG.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Optional flattened DAG locator, e.g. dag:<dag_key>. Omit to scan recent DAGs."),
			"dag_key": StringSchema("Optional DAG key to diagnose. Omit to scan recent DAGs."),
			"limit":   IntegerSchema("Optional DAG scan limit when dag_key is omitted."),
		}), HandleDiagnoseDAGPromptIdentityGaps(svc), "workflow.dag.diagnose_identity", "workflow.definition.read"),
	)
}

// workflowDiagnosticsToolDefinition 注册只读工作台诊断工具。
// 它只读取运行快照和节点摘要，定位符缺失时由 handler 拒绝无界扫描。
func workflowDiagnosticsToolDefinition(svc workflowDiagnosticsPort) ToolDefinition {
	return defineTaskReadTool("task_workflow_diagnostics", "Lookup compact workflow diagnostics by trace_id, run_key, run_id, node_key, or child_thread_id. Returns derived run summaries and matching runtime nodes only.", ObjectSchema(map[string]Schema{
		"pos":             StringSchema("Optional flattened run locator, e.g. dag:<dag_key>/run:<run_key>."),
		"trace_id":        StringSchema("Trace id to find in run events/metadata or node config/result."),
		"run_key":         StringSchema("Exact run key."),
		"run_id":          IntegerSchema("Runtime run id."),
		"node_key":        StringSchema("Runtime node key."),
		"child_thread_id": StringSchema("Child agent thread id spawned by a DAG node."),
		"limit":           IntegerSchema("Max diagnostic rows when scanning recent DAGs."),
	}), HandleWorkflowDiagnostics(svc), "workflow.diagnostics.query", "workflow.runtime.read")
}

// workflowRecoveryActionToolDefinition 注册受控恢复工具。
// cancel_with_cleanup 会落到终止运行；retry_failed_node 先保留 schema 和阻断语义。
func workflowRecoveryActionToolDefinition(svc workflowRecoveryPort) ToolDefinition {
	return defineTaskWriteTool("task_workflow_recovery_action", "Run a controlled workflow recovery action. cancel_with_cleanup is wired to task_terminate_dag; retry_failed_node is validated but blocked until the runtime reset/retry contract exists.", ObjectSchema(map[string]Schema{
		"pos":      StringSchema("Optional flattened run locator, e.g. dag:<dag_key>/run:<run_key>."),
		"action":   EnumStringSchema("Recovery action.", recoveryActionEnum...),
		"dag_key":  StringSchema("DAG key for cancellation fence."),
		"run_key":  StringSchema("Run key for cancel_with_cleanup."),
		"run_id":   IntegerSchema("Run id for retry_failed_node."),
		"node_key": StringSchema("Node key for retry_failed_node."),
		"reason":   StringSchema("Optional user-visible reason."),
	}, "action"), HandleWorkflowRecoveryAction(svc), "workflow.recovery.action", ToolIdempotencyRecommended, "workflow.runtime.write")
}
