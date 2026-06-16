package tools

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// orchestrationToolDefinitions 处理orchestration工具definitions。
func orchestrationToolDefinitions(svc contract.OrchestrationService) []ToolDefinition {
	return buildToolDefinitions(
		defineTool("launch_agent", "Launch a managed orchestration agent.", ObjectSchema(map[string]Schema{
			"agent_id":     StringSchema("Stable persisted orchestration agent ID for this subtask. Reuse the same agent_id when polling or retrying the same subtask; omit only when intentionally launching a separate parallel agent. An active duplicate agent_id returns the existing agent instead of launching another."),
			"name":         StringSchema("User-facing agent name. Prefer a short friendly name tied to the task; avoid paths, IDs, and generic labels like worker-agent."),
			"prompt":       StringSchema("Optional initial prompt to submit as the launched agent's first turn."),
			"parent_id":    StringSchema("Optional parent agent ID for child-agent launches."),
			"agent_type":   StringSchema("Optional stable agent identity for child-agent routing; display name is not used as a fallback."),
			"agent_key":    StringSchema("Optional router agent_key. When set, thread/start looks up the matching prompt_template and injects its assembled sections as base_instructions."),
			"prompt_key":   StringSchema("Optional exact prompt_template.prompt_key to launch. Prefer this for available experts so user-created templates with shared agent_key remain addressable."),
			"memory_scope": EnumStringSchema("Optional child-agent scope metadata for launches.", "project", "user", "local"),

			"cwd":                  StringSchema("Optional only when parent_id resolves to an existing parent agent with cwd; otherwise required. Use an explicit absolute project or workspace path."),
			"provider":             EnumStringSchema("Provider for the launched agent. Defaults to codex when omitted.", launchAgentProviderEnum...),
			"model":                StringSchema("Optional model identifier for the launched agent (e.g. 'sonnet', 'opus', 'claude-opus-4-7[1m]'). When omitted, the provider falls back to its own default (for claude: ~/.claude/settings.json `model`)."),
			"codex_home":           StringSchema("Optional explicit Codex home for codex launches. When any Codex identity override is supplied, codex_home, codex_instance_key, and codex_model_provider must all be supplied."),
			"codex_instance_key":   StringSchema("Optional Codex instance key for codex launches. Use with codex_home and codex_model_provider."),
			"codex_model_provider": StringSchema("Optional Codex CLI model_provider for codex launches (for example, openai). Forwarded as config.codexModelProvider, not as the top-level provider."),
			"effort":               StringSchema("Optional reasoning effort for the launched agent (e.g. xhigh/high/medium/low for codex, max/high/medium/low for claude)."),
			"language":             StringSchema("Optional language tag for the launched agent (e.g. 'zh', 'en'). Propagated to BuildCtx.Language for prompt match_when / section enable_when evaluation."),
			"disabled_tools":       StringSchema("Optional comma-separated list of tool names to disable for the launched agent. Merged with the default deny list."),
		}, "name"), HandleLaunchAgent(svc)),
		defineTool("send_message", "Submit a text turn to an existing orchestration agent.", ObjectSchema(map[string]Schema{
			"pos":      StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id": StringSchema("Target orchestration agent ID."),
			"message":  StringSchema("Message content to submit as a text input."),
		}, "message"), HandleSendMessage(svc)),
		defineTool("stop_agent", "Stop and recycle an orchestration agent by archiving its persisted thread when available.", ObjectSchema(map[string]Schema{
			"pos":      StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id": StringSchema("Target orchestration agent ID."),
		}), HandleStopAgent(svc)),
		defineTool("list_agents", "List orchestration agents and current runtime snapshots. Defaults to active agents only and omits report bodies; use get_agent_report for full reports.", ObjectSchema(map[string]Schema{
			"state":            StringSchema("Optional state filter, e.g. idle, turn_running, stopped. Comma-separated values are accepted."),
			"cwd":              StringSchema("Optional absolute cwd filter. When trusted tool-call scope includes _cwd, list_agents defaults to that trusted _cwd and uses it instead of this argument."),
			"include_inactive": BooleanSchema("Include stopped/failed historical agents. Defaults to false."),
			"include_reports":  BooleanSchema("Include last_report bodies in list output. Defaults to false; prefer get_agent_report."),
			"limit":            IntegerSchema("Maximum number of agents to return after filtering. 0 means no explicit limit."),
			"envelope":         BooleanSchema("When true, return {agents,data,total,showing,truncated,hint}; default false keeps the legacy array response."),
		}), HandleListAgents(svc)),
		defineTool("get_agent_report", "Read the current report snapshot for an orchestration agent. Pass wait=true when a parent agent must block for a child report. Pass the persisted agent_id returned by launch/list; display name is not an identifier.", ObjectSchema(map[string]Schema{
			"pos":          StringSchema("Flattened agent locator, e.g. agent:<agent_id>. Preferred over legacy agent_id."),
			"agent_id":     StringSchema("Persisted target orchestration agent ID returned by launch/list; do not pass name."),
			"wait":         BooleanSchema("Defaults to false. When true, wait until a report, failed/stopped fallback, or timeout."),
			"requester_id": StringSchema("Optional explicit parent/requester agent id. Defaults to the trusted tool scope agent_id when available."),
			"timeout_ms":   IntegerSchema("Optional maximum wait in milliseconds when wait=true. Defaults to the RPC request timeout."),
		}), HandleGetAgentReport(svc)),
	)
}
