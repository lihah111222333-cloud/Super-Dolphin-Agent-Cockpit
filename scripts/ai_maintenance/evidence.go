package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// validateEvidenceFile 对 worker evidence 做 fail-fast 校验；任何缺失或不匹配都返回 BLOCK。
func validateEvidenceFile(path string, plan gatePlan) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read evidence: %w", err)
	}
	doc := parseEvidence(string(data))
	problems := evidenceProblems(doc, plan)
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("AI maintenance evidence BLOCK:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

// evidenceProblems 聚合 evidence 的所有阻断项；调用方负责排序和格式化输出。
func evidenceProblems(doc evidenceDoc, plan gatePlan) []string {
	problems := statusEvidenceProblems(doc)
	if doc.Status == "BLOCKED" || doc.Status == "PLAN_BLOCKER" {
		return problems
	}
	problems = append(problems, completionEvidenceProblems(doc, plan)...)
	return problems
}

func completionEvidenceProblems(doc evidenceDoc, plan gatePlan) []string {
	var problems []string
	problems = append(problems, commandEvidenceProblems(doc.CommandsRun)...)
	problems = append(problems, lspEvidenceProblems(doc, plan)...)
	problems = append(problems, diffScopeProblems(doc, plan)...)
	problems = append(problems, generatedEvidenceProblems(doc, plan)...)
	problems = append(problems, gateCommandProblems(doc, plan)...)
	return problems
}

// commandEvidenceProblems 校验每条命令都有命令文本和成功退出码。
func commandEvidenceProblems(commands []evidenceCommand) []string {
	var problems []string
	for _, cmd := range commands {
		if strings.TrimSpace(cmd.Cmd) == "" {
			problems = append(problems, "COMMANDS_RUN entry missing cmd")
		}
		if cmd.Exit == nil {
			problems = append(problems, "COMMANDS_RUN entry missing exit")
		} else if *cmd.Exit != 0 {
			problems = append(problems, fmt.Sprintf("COMMANDS_RUN %q exit=%d", cmd.Cmd, *cmd.Exit))
		}
	}
	return problems
}

func gateCommandProblems(doc evidenceDoc, plan gatePlan) []string {
	var problems []string
	for _, gate := range plan.RequiredGates {
		if !commandForGatePresent(doc.CommandsRun, gate) && gate != "diff:whitespace" {
			problems = append(problems, "missing command evidence for "+gate)
		}
	}
	return problems
}

// parseEvidence 解析 worker 结尾的 text/yaml 风格 evidence 块。
// 解析器只接受少量明确字段，未知字段不会放宽必填校验。
func parseEvidence(text string) evidenceDoc {
	parser := evidenceParser{doc: evidenceDoc{LSPEvidence: map[string]string{}}}
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		parser.applyLine(strings.TrimSpace(scanner.Text()))
	}
	return parser.doc
}

type evidenceParser struct {
	doc              evidenceDoc
	section          string
	currentCommand   *evidenceCommand
	currentGenerated *evidenceGeneratedFile
}

// applyLine 处理 evidence 块的一行，支持顶层字段、section 标题和列表项三种形态。
func (p *evidenceParser) applyLine(line string) {
	if line == "" || line == "```" || line == "```text" || line == "```yaml" {
		return
	}
	if strings.HasSuffix(line, ":") && !strings.HasPrefix(line, "-") {
		p.section = strings.TrimSuffix(line, ":")
		p.currentCommand = nil
		p.currentGenerated = nil
		return
	}
	if key, value, ok := splitKeyValue(line); ok {
		p.applyKeyValue(key, value)
		return
	}
	if item, ok := strings.CutPrefix(line, "- "); ok {
		p.applyListItem(strings.TrimSpace(item))
	}
}

// applyKeyValue 将 key/value 分派到 evidence doc；未知 key 只在 LSP_EVIDENCE section 中采纳。
func (p *evidenceParser) applyKeyValue(key, value string) {
	if p.applyScalarKey(key, value) {
		return
	}
	if p.applyStructuredKey(key, value) {
		return
	}
	if p.section == "LSP_EVIDENCE" {
		p.doc.LSPEvidence[key] = value
	}
}

func (p *evidenceParser) applyScalarKey(key, value string) bool {
	targets := map[string]*string{
		"PACKAGE":   &p.doc.Package,
		"STATUS":    &p.doc.Status,
		"AGENTID":   &p.doc.AgentID,
		"BASE_HEAD": &p.doc.BaseHead,
	}
	target, ok := targets[key]
	if ok {
		*target = value
	}
	return ok
}

// applyStructuredKey 处理会追加列表或依赖当前 section 的 evidence 字段。
func (p *evidenceParser) applyStructuredKey(key, value string) bool {
	switch key {
	case "OWNED_FILES_CHANGED", "UNRELATED_DIRTY_FILES_PRESERVED", "BLOCKERS":
		p.applyInlineList(key, value)
	case "cmd":
		p.startCommand(value)
	case "exit":
		p.setCommandExit(value)
	case "path":
		p.startGeneratedFile(value)
	case "source_command":
		p.setGeneratedSource(value)
	case "precheck_failed":
		p.setGeneratedPrecheck(value)
	default:
		return false
	}
	return true
}

// applyListItem 解析 section 内的短列表，命令和生成物列表会继续走 key/value 分派。
func (p *evidenceParser) applyListItem(item string) {
	switch p.section {
	case "OWNED_FILES_CHANGED":
		p.doc.OwnedFilesChanged = append(p.doc.OwnedFilesChanged, item)
	case "UNRELATED_DIRTY_FILES_PRESERVED":
		p.doc.UnrelatedDirtyFilesPreserved = append(p.doc.UnrelatedDirtyFilesPreserved, item)
	case "BLOCKERS":
		p.doc.Blockers = append(p.doc.Blockers, item)
	case "COMMANDS_RUN", "GENERATED_FILES":
		if key, value, ok := splitKeyValue(item); ok {
			p.applyKeyValue(key, value)
		}
	}
}

func (p *evidenceParser) applyInlineList(key, value string) {
	values := parseInlineList(value)
	switch key {
	case "OWNED_FILES_CHANGED":
		p.doc.OwnedFilesChanged = append(p.doc.OwnedFilesChanged, values...)
	case "UNRELATED_DIRTY_FILES_PRESERVED":
		p.doc.UnrelatedDirtyFilesPreserved = append(p.doc.UnrelatedDirtyFilesPreserved, values...)
	case "BLOCKERS":
		p.doc.Blockers = append(p.doc.Blockers, values...)
	}
}

func (p *evidenceParser) startCommand(value string) {
	if p.section != "COMMANDS_RUN" {
		return
	}
	p.doc.CommandsRun = append(p.doc.CommandsRun, evidenceCommand{Cmd: value})
	p.currentCommand = &p.doc.CommandsRun[len(p.doc.CommandsRun)-1]
}

func (p *evidenceParser) setCommandExit(value string) {
	if p.section != "COMMANDS_RUN" || p.currentCommand == nil {
		return
	}
	if exit, err := strconv.Atoi(value); err == nil {
		p.currentCommand.Exit = &exit
	}
}

func (p *evidenceParser) startGeneratedFile(value string) {
	if p.section != "GENERATED_FILES" {
		return
	}
	p.doc.GeneratedFiles = append(p.doc.GeneratedFiles, evidenceGeneratedFile{Path: value})
	p.currentGenerated = &p.doc.GeneratedFiles[len(p.doc.GeneratedFiles)-1]
}

func (p *evidenceParser) setGeneratedSource(value string) {
	if p.section == "GENERATED_FILES" && p.currentGenerated != nil {
		p.currentGenerated.SourceCommand = value
	}
}

func (p *evidenceParser) setGeneratedPrecheck(value string) {
	if p.section == "GENERATED_FILES" && p.currentGenerated != nil {
		p.currentGenerated.Precheck = value
	}
}

// commandForGatePresent 将 gate 名称映射到 evidence 中必须出现的命令片段。
func commandForGatePresent(commands []evidenceCommand, gate string) bool {
	wants := gateEvidenceCommandFragments()
	for _, cmd := range commands {
		for _, want := range wants[gate] {
			if strings.Contains(cmd.Cmd, want) {
				return true
			}
		}
	}
	return false
}

func generatedEvidencePresent(files []evidenceGeneratedFile, path string) bool {
	for _, file := range files {
		if file.Path == path && strings.Contains(file.Precheck, "check") && strings.Contains(file.SourceCommand, "refresh") {
			return true
		}
	}
	return false
}

func evidencePassed(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	return value == "PASS" || value == "0" || value == "OK"
}

func splitKeyValue(line string) (string, string, bool) {
	line = strings.TrimPrefix(line, "- ")
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), `"'`), true
}

func parseInlineList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "[]" {
		return nil
	}
	value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(strings.TrimSpace(part), `"'`)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
