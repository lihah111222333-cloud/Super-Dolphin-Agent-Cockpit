package remoteci

import (
	"strings"
)

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellCommands(source []byte) [][]string {
	commands := make([][]string, 0)
	heredoc := ""
	arrayDepth := 0
	for _, line := range strings.Split(string(source), "\n") {
		commandLines, nextHeredoc, nextArrayDepth := workerExecutionShellLine(line, heredoc, arrayDepth)
		heredoc, arrayDepth = nextHeredoc, nextArrayDepth
		commands = append(commands, commandLines...)
	}
	return commands
}

// workerExecutionShellLine 解析一行 shell，并保留跨行 heredoc 与数组状态。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellLine(line, heredoc string, arrayDepth int) ([][]string, string, int) {
	trimmed := strings.TrimSpace(line)
	if heredoc != "" {
		return nil, workerExecutionNextHeredoc(trimmed, heredoc), arrayDepth
	}
	if delimiter := workerExecutionHeredocDelimiter(trimmed); delimiter != "" {
		return nil, delimiter, arrayDepth
	}
	if arrayDepth > 0 {
		return nil, "", arrayDepth + strings.Count(trimmed, "(") - strings.Count(trimmed, ")")
	}
	if depth := workerExecutionArrayDepth(trimmed); depth > 0 {
		return nil, "", depth
	}
	return workerExecutionShellLineCommands(trimmed), "", 0
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionNextHeredoc(line, heredoc string) string {
	if line == heredoc {
		return ""
	}
	return heredoc
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionArrayDepth(line string) int {
	if !strings.Contains(line, "=(") || strings.Contains(line, "$(") {
		return 0
	}
	return strings.Count(line, "(") - strings.Count(line, ")")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellLineCommands(line string) [][]string {
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	commands := make([][]string, 0)
	for _, segment := range workerExecutionShellSegments(line) {
		command := workerExecutionShellCommand(segment)
		if len(command) > 0 && workerExecutionLooksLikeCommand(command) {
			commands = append(commands, command)
		}
	}
	return commands
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionHeredocDelimiter(line string) string {
	_, value, ok := strings.Cut(line, "<<")
	if !ok {
		return ""
	}
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "-")
	if fields := strings.Fields(value); len(fields) > 0 {
		return strings.Trim(fields[0], "\"'")
	}
	return ""
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellSegments(line string) []string {
	line = strings.ReplaceAll(line, "&&", "\n")
	line = strings.ReplaceAll(line, "||", "\n")
	line = strings.ReplaceAll(line, ";", "\n")
	return strings.Split(line, "\n")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellCommand(segment string) []string {
	fields := strings.Fields(strings.TrimSpace(segment))
	fields = workerExecutionTrimShellPrefixes(fields)
	fields = workerExecutionNormalizeShellFields(fields)
	if len(fields) == 0 {
		return nil
	}
	return workerExecutionNormalizeShellCommand(fields)
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionTrimShellPrefixes(fields []string) []string {
	for len(fields) > 0 {
		fields[0] = strings.TrimLeft(fields[0], "@+-(")
		if !workerExecutionShellPrefix(fields[0]) {
			return fields
		}
		fields = fields[1:]
	}
	return nil
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellPrefix(value string) bool {
	return value == "" || value == "if" || value == "then" || value == "do" || value == "!" || strings.Contains(value, "=")
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionNormalizeShellFields(fields []string) []string {
	for index := range fields {
		if fields[index] != "$(MAKE)" {
			fields[index] = strings.Trim(fields[index], "\"'`(){}")
		}
	}
	return fields
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionNormalizeShellCommand(fields []string) []string {
	if fields[0] == "source" || fields[0] == "." {
		return workerExecutionShellSource(fields)
	}
	if workerExecutionRealGoCommand(fields) {
		return append([]string{"go"}, fields[1:]...)
	}
	if fields[0] == "$(MAKE)" {
		fields[0] = "make"
	}
	return fields
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionShellSource(fields []string) []string {
	if len(fields) < 2 {
		return nil
	}
	return []string{"sh", fields[1]}
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionRealGoCommand(fields []string) bool {
	if len(fields) <= 2 || !strings.Contains(fields[0], "real_go") {
		return false
	}
	return fields[1] == "run" || fields[1] == "test"
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func parseWorkerExecutionMakefile(source []byte) *workerExecutionMakefile {
	result := &workerExecutionMakefile{
		targets:   make(map[string]workerExecutionMakeTarget),
		variables: make(map[string]workerExecutionMakeVariable),
	}
	lines := strings.Split(string(source), "\n")
	for index := 0; index < len(lines); {
		line := lines[index]
		if name, value, ok := workerExecutionMakeAssignment(line); ok {
			result.variables[name] = workerExecutionMakeVariable{
				name: name, value: value, content: []byte(line + "\n"),
			}
			index++
			continue
		}
		names, dependencies, ok := workerExecutionMakeTargetHeader(line)
		if !ok {
			index++
			continue
		}
		end := index + 1
		for end < len(lines) && strings.HasPrefix(lines[end], "\t") {
			end++
		}
		content := []byte(strings.Join(lines[index:end], "\n") + "\n")
		for _, name := range names {
			result.targets[name] = workerExecutionMakeTarget{
				name: name, dependencies: dependencies, content: content,
			}
		}
		index = end
	}
	return result
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionMakeAssignment(line string) (string, string, bool) {
	if strings.HasPrefix(line, "\t") {
		return "", "", false
	}
	for _, operator := range []string{":=", "?=", "+=", "="} {
		index := strings.Index(line, operator)
		if index <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:index])
		if name == "" || strings.ContainsAny(name, " \t:") {
			return "", "", false
		}
		return name, strings.TrimSpace(line[index+len(operator):]), true
	}
	return "", "", false
}

// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
// 保持 Worker 执行契约计算的确定性与 fail-fast 语义。
func workerExecutionMakeTargetHeader(line string) ([]string, []string, bool) {
	if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return nil, nil, false
	}
	index := strings.Index(line, ":")
	if index <= 0 || strings.HasPrefix(line[index:], ":=") {
		return nil, nil, false
	}
	names := strings.Fields(strings.TrimSpace(line[:index]))
	if len(names) == 0 {
		return nil, nil, false
	}
	dependencies := make([]string, 0)
	for dependency := range strings.FieldsSeq(strings.TrimSpace(line[index+1:])) {
		if dependency == "|" {
			continue
		}
		dependencies = append(dependencies, dependency)
	}
	return names, dependencies, true
}
