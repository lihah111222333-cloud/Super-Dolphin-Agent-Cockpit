package tools

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

const (
	taskCreateDAGDescription = "Create a new DAG template and its nodes in the orchestration store. " +
		"Existing dag_key values are not replaced; scheduled triggers must be enabled later via " +
		"task_dag_apply_ops with base_version and cron_expr. This does not start execution; " +
		"if the user asked to run/execute now, call task_start_dag after create succeeds."
	taskApplyOpsDescription = "Apply a typed ops batch or one flat action (add_node / update_node / " +
		"remove_node / update_dag) with base_version OCC. Use action+flat fields for common edits; " +
		"use ops for advanced raw batches."
	taskDispatchNodeDescription = "Explicitly assign an agent to a pending/ready DAG node and enqueue " +
		"a wakeup so the dispatcher launches it. Use when a node has assigned_to='' (ADR-004 §Open Q1)."
	taskStartDAGDescription = "Trigger a DAG execution (creates a run and snapshots dag.version). " +
		"The response includes run_id, scheduled_wakeups, and execution_state; if scheduled_wakeups=0 " +
		"with waiting_for_assignee, call task_dispatch_node."
	taskTerminateDAGDescription = "Cancel one running DAG execution by run_key. This marks non-terminal " +
		"runtime nodes cancelled and stops pending/dispatching/sent wakeups."
	taskDeleteDAGDescription = "Delete a DAG template and its completed run history. " +
		"Refuses deletion while any run is active."
	taskListDAGsDescription = "List DAGs for console views and final_output retention checks."
	taskGetRunDescription   = "Fetch a single DAG run by run_key, including the run's runtime node snapshot. " +
		"task_get_dag reads the DAG template."
	taskListRunsDescription = "List recent runs for a DAG (object response wraps the runs slice for " +
		"forward-compatibility). Status enum mirrors migration 0080 task_dag_runs.status CHECK."
	taskDiagnosePromptIdentityDescription = "Read-only diagnostic for historical DAG nodes missing " +
		"prompt_key/agent_key or hybrid verifier provider/Codex identity. It never rewrites DAGs; " +
		"use task_dag_apply_ops for explicit rebind or recreate the DAG."

	dagLocatorDescription = "Flattened DAG locator, e.g. dag:<dag_key>. Preferred over legacy dag_key."
	runLocatorDescription = "Flattened run locator, e.g. run:<run_key> or dag:<dag_key>/run:<run_key>. " +
		"Preferred over legacy run_key."
	runtimeNodeLocatorDescription = "Flattened runtime-node locator, " +
		"e.g. dag:<dag_key>/run_id:<run_id>/node:<node_key>. " +
		"Preferred over legacy dag_key/node_key/run_id."
)

// taskToolDefinitions 处理任务工具definitions。
func taskToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"task_create_dag",
			taskCreateDAGDescription,
			createDAGSchema(),
			HandleCreateDAG(svc),
		),
		defineTool("task_dag_apply_ops", taskApplyOpsDescription, ObjectSchema(map[string]Schema{
			"pos":          StringSchema(dagLocatorDescription),
			"dag_key":      StringSchema("Target DAG key."),
			"base_version": IntegerSchema("Expected current dag.version (OCC; mismatch returns conflict)."),
			"action": EnumStringSchema(
				"Flat action. Omit or use apply_ops_raw with ops for legacy raw batches.",
				applyOpsActionEnum...,
			),
			"ops":         ArraySchema(applyOpsOpSchema(), "Typed ops array; each item must include 'op' discriminator."),
			"node_key":    StringSchema("Target node key for add_node/update_node/remove_node."),
			"title":       StringSchema("Flat title for add_node/update_node/update_dag."),
			"description": StringSchema("Flat DAG description for action=update_dag."),
			"trigger":     StringSchema("Flat DAG trigger for action=update_dag."),
			"cron_expr":   StringSchema("Flat DAG cron expression for action=update_dag."),
			"owner_id":    StringSchema("Flat DAG owner id for action=update_dag."),
			"node_type":   EnumStringSchema("Node type for action=add_node.", "agent", "automation", "hybrid"),
			"assigned_to": StringSchema("Flat node assignee for action=update_node."),
			"depends_on": ArraySchema(
				StringSchema("Dependency node key."),
				"Flat node dependencies for add_node/update_node.",
			),
			"config": RawObjectSchema("Flat node config for add_node/update_node."),
			"patch":  RawObjectSchema("Advanced raw patch object for update_dag/update_node."),
		}, "base_version"), HandleApplyOps(svc)),
		defineTool("task_update_node", "Update the runtime status for a DAG node.", ObjectSchema(map[string]Schema{
			"pos":      StringSchema(runtimeNodeLocatorDescription),
			"dag_key":  StringSchema("DAG key."),
			"node_key": StringSchema("Node key within the DAG."),
			"run_id":   IntegerSchema("Task DAG run id that owns the runtime node."),
			"status":   EnumStringSchema("New node status.", updateNodeStatusEnum...),
			"result":   StringSchema("Optional result summary."),
		}, "status"), HandleUpdateNode(svc)),
		defineTool("task_dispatch_node", taskDispatchNodeDescription, ObjectSchema(map[string]Schema{
			"pos":         StringSchema(runtimeNodeLocatorDescription),
			"dag_key":     StringSchema("DAG key."),
			"node_key":    StringSchema("Node key within the DAG."),
			"run_id":      IntegerSchema("Task DAG run id that owns the runtime node."),
			"assigned_to": StringSchema("Agent id to dispatch the node to."),
		}, "assigned_to"), HandleDispatchNode(svc)),
		defineTool("task_start_dag", taskStartDAGDescription, ObjectSchema(map[string]Schema{
			"pos":             StringSchema(dagLocatorDescription),
			"dag_key":         StringSchema("DAG to start."),
			"trigger_source":  EnumStringSchema("Trigger source.", startDAGTriggerEnum...),
			"idempotency_key": StringSchema("Optional, prevents duplicate run on retry."),
		}), HandleStartDAG(svc)),
		defineTool("task_terminate_dag", taskTerminateDAGDescription, ObjectSchema(map[string]Schema{
			"pos":     StringSchema("Flattened DAG run locator, e.g. dag:<dag_key>/run:<run_key>."),
			"dag_key": StringSchema("DAG key used as a fence for the run."),
			"run_key": StringSchema("Run key to cancel."),
			"reason":  StringSchema("Optional cancellation reason."),
		}), HandleTerminateDAG(svc)),
		defineTool("task_delete_dag", taskDeleteDAGDescription, ObjectSchema(map[string]Schema{
			"pos":     StringSchema(dagLocatorDescription),
			"dag_key": StringSchema("DAG key to delete."),
		}), HandleDeleteDAG(svc)),
		defineTool("task_list_dags", taskListDAGsDescription, ObjectSchema(map[string]Schema{
			"status":  EnumStringSchema("Optional status filter.", listDAGsStatusEnum...),
			"keyword": StringSchema("Optional keyword filter."),
			"limit":   IntegerSchema("Optional max rows; defaults to service limit when 0/omitted."),
		}), HandleListDAGs(svc)),
		defineTool("task_get_dag", "Fetch a DAG and all of its nodes.", ObjectSchema(map[string]Schema{
			"pos":     StringSchema(dagLocatorDescription),
			"dag_key": StringSchema("Unique DAG key."),
		}), HandleGetDAG(svc)),
		defineTool("task_get_run", taskGetRunDescription, ObjectSchema(map[string]Schema{
			"pos":     StringSchema(runLocatorDescription),
			"run_key": StringSchema("Run key returned by task_start_dag."),
		}), HandleGetRun(svc)),
		defineTool("task_list_runs", taskListRunsDescription, ObjectSchema(map[string]Schema{
			"pos":     StringSchema(dagLocatorDescription),
			"dag_key": StringSchema("DAG key to list runs for."),
			"status":  EnumStringSchema("Optional status filter.", listRunsStatusEnum...),
			"limit":   IntegerSchema("Optional max rows; defaults to 50 when 0/omitted."),
		}), HandleListRuns(svc)),
		defineTool(
			"task_diagnose_dag_prompt_identity_gaps",
			taskDiagnosePromptIdentityDescription,
			ObjectSchema(map[string]Schema{
				"pos":     StringSchema("Optional flattened DAG locator, e.g. dag:<dag_key>. Omit to scan recent DAGs."),
				"dag_key": StringSchema("Optional DAG key to diagnose. Omit to scan recent DAGs."),
				"limit":   IntegerSchema("Optional DAG scan limit when dag_key is omitted."),
			}),
			HandleDiagnoseDAGPromptIdentityGaps(svc),
		),
	)
}
