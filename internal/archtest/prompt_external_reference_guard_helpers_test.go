package archtest_test

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func promptExternalReferenceViolations(surfaces []promptSurface) []string {
	var violations []string
	for _, surface := range surfaces {
		violations = append(violations, surfaceExternalReferenceViolations(surface)...)
	}
	sort.Strings(violations)
	return violations
}

func surfaceExternalReferenceViolations(surface promptSurface) []string {
	text := strings.TrimSpace(surface.Text)
	if text == "" {
		return nil
	}
	var violations []string
	violations = append(violations, identityClaimViolations(surface, text)...)
	violations = append(violations, externalToolViolations(surface, text)...)
	violations = append(violations, hostAssumptionViolations(surface, text)...)
	violations = append(violations, internalToolViolations(surface)...)
	return violations
}

func identityClaimViolations(surface promptSurface, text string) []string {
	var violations []string
	for _, match := range externalIdentityClaimMatches(text) {
		if isNegativeBoundaryContext(text, match.Start) || isProviderContextRule(text, match.Start) {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s: external provider identity claim %q", surface.Source, match.Text))
	}
	return violations
}

func externalToolViolations(surface promptSurface, text string) []string {
	return positiveReferenceViolations(surface.Source, text, positiveExternalToolProtocolMatches(text), "external tool protocol assumption")
}

func hostAssumptionViolations(surface promptSurface, text string) []string {
	return positiveReferenceViolations(surface.Source, text, positiveHostAssumptionMatches(text), "fixed host assumption")
}

func positiveReferenceViolations(source, text string, matches []textMatch, label string) []string {
	var violations []string
	for _, match := range matches {
		if isNegativeBoundaryContext(text, match.Start) {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s: %s %q", source, label, match.Text))
	}
	return violations
}

func internalToolViolations(surface promptSurface) []string {
	if surface.CleanupContext || surface.AllowedToolUse {
		return nil
	}
	violations := make([]string, 0, len(surface.InternalTools))
	for _, tool := range surface.InternalTools {
		violations = append(violations, fmt.Sprintf("%s: ungated resident internal tool assumption %q", surface.Source, tool))
	}
	return violations
}

type textMatch struct {
	Text  string
	Start int
}

var (
	englishIdentityClaimPattern = regexp.MustCompile(`(?i)\b(?:you are|i am|i'm)\s+(?:the\s+)?(?:claude(?: code)?|codex|gpt(?:[- ][[:alnum:]]+)?|cursor|kiro|warp|github copilot|traycer|cluely)\b`)
	chineseIdentityClaimPattern = regexp.MustCompile(`(?:我是|你是)(?:「|“|")?(?:Claude(?: Code)?|Codex|GPT|Cursor|Kiro|Warp|GitHub Copilot|Traycer|Cluely)(?:」|”|")?`)
	providerBylinePattern       = regexp.MustCompile(`(?i)\b(?:provided|built|created|made)\s+by\s+(?:anthropic|openai)\b|由\s*(?:Anthropic|OpenAI)\s*(?:提供|开发|构建)`)
	externalToolProtocolPattern = regexp.MustCompile(`(?i)(?:\b(?:use|call|invoke|must use|available|tool is available)\b|使用|调用|可以通过|可用)[^.。;\n]{0,80}\b(?:WebFetch|run_command|read_files)\b|\b(?:WebFetch|run_command|read_files)\b[^.。;\n]{0,80}(?:\btool is available\b|可用)|\b(?:IDE-only approval|external approval schema)\b`)
	hostAssumptionPattern       = regexp.MustCompile(`(?i)(?:屏幕可见|可以看到屏幕|可以听到音频|音频可见|must be terminal-only|must use no-browser|IDE sidebar is available|browser extension is available|浏览器扩展已启用|浏览器扩展已经可用|可以使用浏览器扩展)`)
	sqlSingleQuotePattern       = regexp.MustCompile(`(?s)'(?:''|[^'])*'`)
	sqlDollarQuotePattern       = regexp.MustCompile(`(?s)\$[A-Za-z0-9_]*\$.*?\$[A-Za-z0-9_]*\$`)
)

func externalIdentityClaimMatches(text string) []textMatch {
	var matches []textMatch
	for _, pattern := range []*regexp.Regexp{
		englishIdentityClaimPattern,
		chineseIdentityClaimPattern,
		providerBylinePattern,
	} {
		matches = append(matches, regexpTextMatches(pattern, text)...)
	}
	return matches
}

func positiveExternalToolProtocolMatches(text string) []textMatch {
	return regexpTextMatches(externalToolProtocolPattern, text)
}

func positiveHostAssumptionMatches(text string) []textMatch {
	return regexpTextMatches(hostAssumptionPattern, text)
}

func regexpTextMatches(pattern *regexp.Regexp, text string) []textMatch {
	indices := pattern.FindAllStringIndex(text, -1)
	matches := make([]textMatch, 0, len(indices))
	for _, index := range indices {
		matches = append(matches, textMatch{Text: text[index[0]:index[1]], Start: index[0]})
	}
	return matches
}

func isNegativeBoundaryContext(text string, start int) bool {
	context := strings.ToLower(sentenceContext(text, start))
	negativeMarkers := []string{
		"do not",
		"don't",
		"dont",
		"not ",
		"never",
		"avoid",
		"without assuming",
		"不要",
		"不得",
		"不能",
		"禁止",
		"避免",
		"不是",
		"不可",
		"不要假设",
		"不要自称",
	}
	for _, marker := range negativeMarkers {
		if strings.Contains(context, marker) {
			return true
		}
	}
	return false
}

func isProviderContextRule(text string, start int) bool {
	context := strings.ToLower(sentenceContext(text, start))
	if !strings.Contains(context, "codex") && !strings.Contains(context, "claude") {
		return false
	}
	providerRuleMarkers := []string{
		"provider",
		"模型",
		"执行任务",
		"directly apply",
		"migration",
		"list_models",
	}
	for _, marker := range providerRuleMarkers {
		if strings.Contains(context, marker) {
			return true
		}
	}
	return false
}

func sentenceContext(text string, start int) string {
	if start < 0 || start > len(text) {
		start = 0
	}
	left := start
	for left > 0 && !strings.ContainsRune(".。!！?\n", rune(text[left-1])) {
		left--
	}
	right := start
	for right < len(text) && !strings.ContainsRune(".。!！?\n", rune(text[right])) {
		right++
	}
	if right < len(text) {
		right++
	}
	return text[left:right]
}

func isRuntimeSystemPrompt(tmpl contract.BuiltinPromptTemplate) bool {
	if !tmpl.Enabled {
		return false
	}
	if !containsString(tmpl.Tags, "builtin:system") {
		return false
	}
	return tmpl.Scope == "global" || strings.HasPrefix(tmpl.Scope, "cwd:")
}

func hasEnableWhen(raw []byte) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != "{}"
}

func internalToolsIn(text string) []string {
	internalTools := append([]string{}, contract.OrchestrationToolCanonicalNames()...)
	internalTools = append(internalTools,
		"orchestration_launch_agent",
		"orchestration_get_agent_report",
		"orchestration_get_agent_reports",
		"orchestration_send_message",
		"orchestration_stop_agent",
		"orchestration_recover_agent",
		"orchestration_interrupt_agent",
		"orchestration_list_agents",
		"list_models",
		"task_create_dag",
		"task_dag_apply_ops",
		"task_get_dag",
		"task_update_node",
		"task_list_runs",
		"prompt_list",
		"command_list",
		"shared_file_list",
		"shared_file_read",
		"shared_file_write",
	)
	var found []string
	for _, tool := range internalTools {
		if strings.Contains(text, tool) {
			found = append(found, tool)
		}
	}
	return found
}

func allowedInternalToolUseInMigrationLiteral(name, literal string) bool {
	if is0108DAGDesignerPromptTextReplacementLiteral(name, literal) {
		return true
	}
	if migrationNumber(name) >= builtinRegistryCutoverMigration {
		return false
	}
	return allowedLegacyPromptSeedMigrationToolUse(name, literal)
}

func is0108DAGDesignerPromptTextReplacementLiteral(name, literal string) bool {
	if !strings.HasPrefix(name, "0108_refresh_dag_designer_prompt_final_node_key.sql") {
		return false
	}
	return slices.Contains(dagDesigner0108PromptTextReplacementLiterals(), literal)
}

func dagDesigner0108PromptTextReplacementLiterals() []string {
	return []string{
		"`task_create_dag(agent_id, dag_key, title, description?, schedule, nodes?)`：新建 DAG。`schedule.trigger ∈ {manual, auto, scheduled}`；scheduled 需要后续 task_dag_apply_ops 写 cron_expr。`agent_id` 填你自己的 orchestration agent id。",
		"`task_create_dag(agent_id, dag_key, title, description?, schedule, final_node_key?, nodes?)`：新建 DAG。`final_node_key` 必须指向唯一的用户可见最终交付节点；run 完成后该节点结果会被索引到 run-level `metadata.final_output`，大结果仍用 Shared Files 承载。`schedule.trigger ∈ {manual, auto, scheduled}`；scheduled 需要后续 task_dag_apply_ops 写 cron_expr。`agent_id` 填你自己的 orchestration agent id。",
		"3. **画 DAG**：在脑子里 (或回复里) 列出节点清单 — 每个节点的 node_key / title / node_type / depends_on / 关键 config。先写文字版给用户看一眼，征得同意再落库。",
		"3. **画 DAG**：在脑子里 (或回复里) 列出节点清单 — 每个节点的 node_key / title / node_type / depends_on / 关键 config，并标明哪个 node_key 是最终交付节点 final_node_key。先写文字版给用户看一眼，征得同意再落库。",
		"5. **展示**：调 task_get_dag 把最终 DAG 读出来，用「节点列表 + 依赖箭头」格式呈现给用户，标明哪个节点是 cron 触发起点 (若有)、哪些节点写 sharedfile。",
		"5. **展示**：调 task_get_dag 把最终 DAG 读出来，用「节点列表 + 依赖箭头」格式呈现给用户，标明哪个节点是 cron 触发起点 (若有)、哪个节点写入 run-level final_output、哪些节点写 sharedfile。",
		"5. **size_cap**：单节点 `result` JSONB 超 4KB 必须走 sharedfile；DAG ≥ 10 节点要在 `inputs.summarization` 上认真填策略 (ADR-006/H7)。骨架阶段 summarization 仅字段位，但你设计时要把它纳入考虑。\n6. **trigger 三种**：",
		"5. **size_cap**：单节点 `result` JSONB 超 4KB 必须走 sharedfile；DAG ≥ 10 节点要在 `inputs.summarization` 上认真填策略 (ADR-006/H7)。骨架阶段 summarization 仅字段位，但你设计时要把它纳入考虑。\n6. **最终产物**：每个新 DAG 只选一个 `final_node_key`，它必须匹配已有 `node_key`，用于把该节点结果提升为 run-level `metadata.final_output`。中间产物可写 sharedfile，但不要让用户去 sharedfile 里找最终答案。\n7. **trigger 三种**：",
		"7. **错误信息一律给到用户**：调工具失败要把错误类型告诉用户 (而不是吞掉重试)，例如 ErrDAGNotFound / ErrVersionConflict / 资源不存在。",
		"8. **错误信息一律给到用户**：调工具失败要把错误类型告诉用户 (而不是吞掉重试)，例如 ErrDAGNotFound / ErrVersionConflict / 资源不存在。",
		"4. 用户确认后，调 `task_create_dag` 一次性建好，schedule.trigger=\"scheduled\"，然后 task_dag_apply_ops 把 cron_expr 设上。",
		"4. 用户确认后，调 `task_create_dag` 一次性建好，传 `final_node_key=\"review\"`，schedule.trigger=\"scheduled\"，然后 task_dag_apply_ops 把 cron_expr 设上。",
		"`task_create_dag(agent_id, dag_key, title, description?, schedule, nodes?)`: Create a DAG. `schedule.trigger ∈ {manual, auto, scheduled}`; scheduled DAGs need a later task_dag_apply_ops call to write cron_expr. Set `agent_id` to your own orchestration agent id.",
		"`task_create_dag(agent_id, dag_key, title, description?, schedule, final_node_key?, nodes?)`: Create a DAG. `final_node_key` must point to the single user-facing final deliverable node; when the run completes, that node result is indexed as run-level `metadata.final_output`, while large payloads still belong in Shared Files. `schedule.trigger ∈ {manual, auto, scheduled}`; scheduled DAGs need a later task_dag_apply_ops call to write cron_expr. Set `agent_id` to your own orchestration agent id.",
		"3. **Sketch the DAG**: Prepare a node list, in your head or in the reply — node_key / title / node_type / depends_on / key config for each node. Show the text sketch to the user and get approval before writing it.",
		"3. **Sketch the DAG**: Prepare a node list, in your head or in the reply — node_key / title / node_type / depends_on / key config for each node, and mark which node_key is the final deliverable node final_node_key. Show the text sketch to the user and get approval before writing it.",
		"5. **Present it**: Call task_get_dag to read the final DAG, then present it as \"node list + dependency arrows\". Mark the cron trigger entry node (if any) and which nodes write sharedfile outputs.",
		"5. **Present it**: Call task_get_dag to read the final DAG, then present it as \"node list + dependency arrows\". Mark the cron trigger entry node (if any), which node writes run-level final_output, and which nodes write sharedfile outputs.",
		"5. **size_cap**: Any single node `result` JSONB over 4KB must use sharedfile. DAGs with 10 or more nodes need a serious `inputs.summarization` strategy (ADR-006/H7). In the skeleton phase, summarization is only a field slot, but account for it in your design.\n6. **Three triggers**:",
		"5. **size_cap**: Any single node `result` JSONB over 4KB must use sharedfile. DAGs with 10 or more nodes need a serious `inputs.summarization` strategy (ADR-006/H7). In the skeleton phase, summarization is only a field slot, but account for it in your design.\n6. **Final deliverable**: Pick exactly one `final_node_key` for each new DAG. It must match an existing `node_key` and is used to promote that node result into run-level `metadata.final_output`. Intermediate artifacts may use sharedfile, but users should not have to search sharedfile for the final answer.\n7. **Three triggers**:",
		"7. **Always surface tool errors to the user**: If a tool call fails, tell the user the error type instead of swallowing it and retrying silently, for example ErrDAGNotFound / ErrVersionConflict / resource not found.",
		"8. **Always surface tool errors to the user**: If a tool call fails, tell the user the error type instead of swallowing it and retrying silently, for example ErrDAGNotFound / ErrVersionConflict / resource not found.",
		"4. After the user confirms, call `task_create_dag` once to create it with schedule.trigger=\"scheduled\", then call task_dag_apply_ops to set cron_expr.",
		"4. After the user confirms, call `task_create_dag` once with `final_node_key=\"review\"` and schedule.trigger=\"scheduled\", then call task_dag_apply_ops to set cron_expr.",
	}
}

func strip0108DAGDesignerPromptTextReplacementLiterals(sql string) string {
	for _, literal := range dagDesigner0108PromptTextReplacementLiterals() {
		sql = strings.ReplaceAll(sql, literal, "dag_designer_final_node_key_replacement")
	}
	return sql
}

func allowedLegacyPromptSeedMigrationToolUse(name, literal string) bool {
	normalized := strings.ToLower(name + " " + literal)
	allowedMarkers := []string{
		"dag_designer",
		"flow designer",
		"流程设计师",
		"orchestrator",
		"orchestration_",
	}
	for _, marker := range allowedMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func appendPromptSurface(surfaces []promptSurface, surface promptSurface) []promptSurface {
	if strings.TrimSpace(surface.Text) == "" {
		return surfaces
	}
	return append(surfaces, surface)
}

func migrationNumber(name string) int {
	match := regexp.MustCompile(`^(\d+)_`).FindStringSubmatch(name)
	if match == nil {
		return -1
	}
	n, err := strconv.Atoi(match[1])
	if err != nil {
		return -1
	}
	return n
}

func isPromptCleanupMigration(name, sql string) bool {
	if strings.HasPrefix(name, "0105_") || strings.HasPrefix(name, "0107_") {
		return true
	}
	normalized := strings.ToLower(sql)
	return strings.Contains(normalized, "delete from public.prompt_templates") ||
		strings.Contains(normalized, "delete from prompt_templates") ||
		strings.Contains(normalized, "delete from public.prompt_routing_tests") ||
		strings.Contains(normalized, "delete from prompt_routing_tests")
}

func writesPromptRuntimeSurface(sql string) bool {
	normalized := strings.ToLower(sql)
	if !strings.Contains(normalized, "prompt_templates") && !strings.Contains(normalized, "prompt_template_sections") {
		return false
	}
	fieldMarkers := []string{
		"prompt_text",
		"when_to_use",
		"description",
		"tags",
		"scope",
		"body",
		"insert into public.prompt_templates",
		"insert into prompt_templates",
		"update public.prompt_templates",
		"update prompt_templates",
		"insert into public.prompt_template_sections",
		"insert into prompt_template_sections",
		"update public.prompt_template_sections",
		"update prompt_template_sections",
	}
	for _, marker := range fieldMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func sqlStringLiterals(sql string) []string {
	var literals []string
	for _, raw := range sqlDollarQuotePattern.FindAllString(sql, -1) {
		literals = append(literals, stripDollarQuote(raw))
	}
	for _, raw := range sqlSingleQuotePattern.FindAllString(sql, -1) {
		literals = append(literals, strings.ReplaceAll(strings.Trim(raw, "'"), "''", "'"))
	}
	return literals
}

func requireSQLTupleForPromptKey(t *testing.T, sql, promptKey string) string {
	t.Helper()

	tuple, ok := sqlTupleForPromptKey(sql, promptKey)
	if !ok {
		t.Fatalf("missing SQL tuple for prompt_key %q", promptKey)
	}
	return tuple
}

func sqlTupleForPromptKey(sql, promptKey string) (string, bool) {
	needle := "'" + promptKey + "'"
	for offset := 0; offset < len(sql); {
		relative := strings.Index(sql[offset:], needle)
		if relative < 0 {
			return "", false
		}
		index := offset + relative
		start := sqlTupleStartBefore(sql, index)
		if start >= 0 {
			end := matchingSQLParen(sql, start)
			if end > start {
				return sql[start : end+1], true
			}
		}
		offset = index + len(needle)
	}
	return "", false
}

func sqlTupleStartBefore(sql string, index int) int {
	for i := index - 1; i >= 0; i-- {
		if sql[i] != '(' {
			continue
		}
		if strings.TrimSpace(sql[i+1:index]) == "" {
			return i
		}
	}
	return -1
}

func matchingSQLParen(sql string, start int) int {
	depth := 0
	for i := start; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			i = skipSingleQuotedSQLLiteral(sql, i)
		case '$':
			i = skipDollarQuotedSQLLiteral(sql, i)
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func skipSingleQuotedSQLLiteral(sql string, start int) int {
	for i := start + 1; i < len(sql); i++ {
		if sql[i] != '\'' {
			continue
		}
		if i+1 < len(sql) && sql[i+1] == '\'' {
			i++
			continue
		}
		return i
	}
	return len(sql) - 1
}

func skipDollarQuotedSQLLiteral(sql string, start int) int {
	tagEnd := strings.IndexByte(sql[start+1:], '$')
	if tagEnd < 0 {
		return start
	}
	tagEnd += start + 2
	tag := sql[start:tagEnd]
	closeIndex := strings.Index(sql[tagEnd:], tag)
	if closeIndex < 0 {
		return start
	}
	return tagEnd + closeIndex + len(tag) - 1
}

func stripDollarQuote(raw string) string {
	firstEnd := strings.Index(raw[1:], "$")
	if firstEnd < 0 {
		return raw
	}
	firstEnd += 2
	lastStart := strings.LastIndex(raw[:len(raw)-1], "$")
	if lastStart <= firstEnd {
		return raw
	}
	return raw[firstEnd:lastStart]
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
