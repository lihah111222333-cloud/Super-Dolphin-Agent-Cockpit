package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	internalTools := []string{
		"orchestration_launch_agent",
		"orchestration_get_agent_report",
		"orchestration_send_message",
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
	}
	var found []string
	for _, tool := range internalTools {
		if strings.Contains(text, tool) {
			found = append(found, tool)
		}
	}
	return found
}

func allowedInternalToolUseInMigrationLiteral(name, literal string) bool {
	if is0106DAGDesignerPromptTextReplacementLiteral(name, literal) {
		return true
	}
	if migrationNumber(name) >= builtinRegistryCutoverMigration {
		return false
	}
	return allowedLegacyPromptSeedMigrationToolUse(name, literal)
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

func readRollback0105Blocks(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "migrations", "ROLLBACK.md"))
	if err != nil {
		t.Fatalf("read migrations/ROLLBACK.md: %v", err)
	}
	content := string(data)
	start := strings.Index(content, "## 0105 — delete unused builtin prompt seeds")
	if start < 0 {
		return ""
	}
	rest := content[start+len("## 0105 — delete unused builtin prompt seeds"):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
