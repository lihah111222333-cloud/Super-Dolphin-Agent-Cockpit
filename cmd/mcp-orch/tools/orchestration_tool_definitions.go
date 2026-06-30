package tools

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// orchestrationToolDefinitions 注册编排工具的 wire schema 和 handler。
// schema 文案需要和 handler 层校验保持一致，避免模型看到的可用字段与运行时不一致。
func orchestrationToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("launch_agent", "Launch a managed orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id":         StringSchema("Stable persisted orchestration agent ID for this subtask. Reuse the same agent_id when polling or retrying the same subtask; omit only when intentionally launching a separate parallel agent. An active duplicate agent_id returns the existing agent instead of launching another."),
			"name":             StringSchema("User-facing agent name. Prefer a short friendly name tied to the task; avoid paths, IDs, and generic labels like worker-agent."),
			"prompt":           StringSchema("Optional initial prompt to submit as the launched agent's first turn."),
			"context_mode":     EnumStringSchema("Optional context mode for the first turn. Defaults to minimal. Use minimal for prompt-only launches; use focused only when the single context field carries caller-selected context; use forked to fork the parent provider thread. Do not copy the parent conversation history unless using forked.", launchAgentContextModeEnum...),
			"context":          StringSchema("Optional focused-mode context selected by the parent agent. Required when context_mode is focused; rejected when context_mode is minimal or forked. Suggested focused context: background, confirmed decisions, relevant file paths, forbidden actions, return format, and known risks. Prefer file paths, function names, line numbers, and constraints. Do not paste large code blocks unless the child cannot read them directly. The child still uses the fixed Markdown report template and must not delegate again."),
			"parent_id":        StringSchema("Optional parent agent ID for child-agent launches."),
			"parent_thread_id": StringSchema("Optional desktop thread ID to fork when context_mode=forked and parent_id belongs to a root or managed agent outside the mcp-orch runtime map."),
			"agent_type":       StringSchema("Optional stable agent identity for child-agent routing; display name is not used as a fallback."),
			"read_only":        BooleanSchema("Optional explicit read-only review or planning delegation flag. When true, launch_agent applies the read-only tool surface and does not change agent_type routing identity."),
			"agent_key":        StringSchema("Optional router agent_key. When set, thread/start looks up the matching prompt_template and injects its assembled sections as base_instructions."),
			"prompt_key":       StringSchema("Optional exact prompt_template.prompt_key to launch. Prefer this for available experts so user-created templates with shared agent_key remain addressable."),
			"memory_scope":     EnumStringSchema("Optional child-agent scope metadata for launches.", "project", "user", "local"),

			"cwd":                  StringSchema("Optional only when parent_id resolves to an existing parent agent with cwd; otherwise required. Use an explicit absolute project or workspace path."),
			"provider":             EnumStringSchema("Provider for the launched agent. Defaults to codex when omitted. Child-agent orchestration with parent_id currently supports codex only; claude is retained only for legacy root-launch compatibility.", launchAgentProviderEnum...),
			"model":                StringSchema("Optional model identifier for the launched agent. For first-version child-agent orchestration, use codex-compatible models."),
			"codex_home":           StringSchema("Optional explicit Codex home for codex launches. When any Codex identity override is supplied, codex_home, codex_instance_key, and codex_model_provider must all be supplied."),
			"codex_instance_key":   StringSchema("Optional Codex instance key for codex launches. Use with codex_home and codex_model_provider."),
			"codex_model_provider": StringSchema("Optional Codex CLI model_provider for codex launches (for example, openai). Forwarded as config.codexModelProvider, not as the top-level provider."),
			"effort":               StringSchema("Optional reasoning effort for the launched agent. For first-version child-agent orchestration, use codex-compatible effort values."),
			"language":             StringSchema("Optional language tag for the launched agent (e.g. 'zh', 'en'). Propagated to BuildCtx.Language for prompt match_when / section enable_when evaluation."),
			"disabled_tools":       StringSchema("Optional comma-separated list of tool names to disable for the launched agent. Merged with the default deny list."),
		}, "name"), HandleLaunchAgent(svc)),
		defineTool("send_message", "Submit a text turn to an existing orchestration agent.", ObjectSchema(map[string]Schema{
			"pos":         StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id":    StringSchema("Target orchestration agent ID."),
			"message":     StringSchema("Message content to submit as a text input."),
			"wait_report": BooleanSchema("Optional. When true, send a follow-up only to an idle agent, then wait for a new report_seq after the pre-submit report. It does not interrupt or queue work."),
			"timeout_ms":  IntegerSchema("Optional maximum wait in milliseconds when wait_report=true. Defaults to the RPC request timeout."),
		}, "message"), HandleSendMessage(svc)),
		defineTool("stop_agent", "Stop and recycle an orchestration agent by archiving its persisted thread when available.", ObjectSchema(map[string]Schema{
			"pos":        StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id":   StringSchema("Target orchestration agent ID."),
			"wait":       BooleanSchema("Optional. When true, wait for stop state settlement after requesting stop/archive."),
			"timeout_ms": IntegerSchema("Optional maximum wait in milliseconds when wait=true. Defaults to the RPC request timeout."),
		}), HandleStopAgent(svc)),
		defineTool("recover_agent", "Recover a stopped or failed orchestration agent and return its latest snapshot.", ObjectSchema(map[string]Schema{
			"pos":      StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id": StringSchema("Target orchestration agent ID."),
		}), HandleRecoverAgent(svc)),
		defineTool("interrupt_agent", "Interrupt the current turn of a running orchestration agent and wait for state settlement.", ObjectSchema(map[string]Schema{
			"pos":        StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id":   StringSchema("Target orchestration agent ID."),
			"source":     StringSchema("Optional interrupt source. Defaults to parent_agent."),
			"timeout_ms": IntegerSchema("Optional maximum wait in milliseconds for idle/stopped/failed settlement. Defaults to the RPC request timeout."),
		}), HandleInterruptAgent(svc)),
		defineTool("list_agents", "List orchestration agents and current runtime snapshots. Defaults to active agents only and omits report bodies; use get_agent_report for one agent or get_agent_reports for multiple reports.", ObjectSchema(map[string]Schema{
			"state":            StringSchema("Optional state filter, e.g. idle, turn_running, stopped. Comma-separated values are accepted."),
			"cwd":              StringSchema("Optional absolute cwd filter. When trusted tool-call scope includes _cwd, list_agents defaults to that trusted _cwd and uses it instead of this argument."),
			"include_inactive": BooleanSchema("Include stopped/failed historical agents. Defaults to false."),
			"include_reports":  BooleanSchema("Include last_report bodies in list output. Defaults to false; use get_agent_report for one target or get_agent_reports for multiple targets."),
			"limit":            IntegerSchema("Maximum number of agents to return after filtering. 0 means no explicit limit."),
			"envelope":         BooleanSchema("When true, return {agents,data,total,showing,truncated,hint}; default false keeps the legacy array response."),
		}), HandleListAgents(svc)),
		defineTool("get_agent_report", "Read the current report snapshot for an orchestration agent. Pass wait=true when a parent agent must block for a child report. Pass the persisted agent_id returned by launch/list; display name is not an identifier.", ObjectSchema(map[string]Schema{
			"pos":              StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id":         StringSchema("Persisted target orchestration agent ID returned by launch/list; do not pass name."),
			"wait":             BooleanSchema("Defaults to false. When true, wait until a report, failed/stopped fallback, or timeout."),
			"requester_id":     StringSchema("Optional explicit parent/requester agent id. Defaults to the trusted tool scope agent_id when available."),
			"timeout_ms":       IntegerSchema("Optional maximum wait in milliseconds when wait=true. Defaults to the RPC request timeout."),
			"after_report_seq": IntegerSchema("Optional when wait=true. If set, return only a report whose report_seq is greater than this value; use it to avoid reading a previous turn's report."),
		}), HandleGetAgentReport(svc)),
		defineTool("get_agent_reports", "Read current report snapshots for multiple orchestration agents, or wait for all target agents to produce reports using one shared timeout. wait=true supports only all semantics; any/quorum/first_success are intentionally unsupported.", ObjectSchema(map[string]Schema{
			"agent_ids":                 ArraySchema(StringSchema("Persisted target orchestration agent ID."), "Persisted target orchestration agent IDs returned by launch/list; display names are not identifiers."),
			"wait":                      BooleanSchema("Defaults to false. When true, wait until all target agents have a report or failed/stopped fallback, or until the shared timeout."),
			"timeout_ms":                IntegerSchema("Optional shared maximum wait in milliseconds when wait=true. Defaults to the RPC request timeout and is not multiplied by agent count."),
			"after_report_seq_by_agent": RawObjectSchema("Optional object mapping agent_id to the last seen report_seq. When wait=true, old reports at or below that seq do not complete that agent."),
		}, "agent_ids"), HandleGetAgentReports(svc)),
	)
}
