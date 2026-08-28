package tools

import (
	"errors"
	"fmt"
	"strings"
)

// validateDiagnosticsInput 校验目标选择和差量过滤的互斥关系。
func validateDiagnosticsInput(input diagnosticsInput) (*diagnosticsFilterRequest, error) {
	if err := validateDiagnosticsTargetSelection(input); err != nil {
		return nil, err
	}
	if len(input.ChangedLines) > 0 && input.DiffFilter != nil {
		return nil, errors.New("diagnostics changed_lines and diff_filter are mutually exclusive")
	}
	if len(input.ChangedLines) > 0 {
		if len(input.FilePaths) > 0 {
			return nil, errors.New("diagnostics changed_lines shorthand is only valid with file_path; use diff_filter.changed_lines for file_paths")
		}
		if err := validateDiagnosticLineNumbers("changed_lines", input.ChangedLines); err != nil {
			return nil, err
		}
		return &diagnosticsFilterRequest{mode: "changed_lines", legacyChangedLines: append([]int(nil), input.ChangedLines...)}, nil
	}
	return validateDiagnosticsDiffFilter(input.DiffFilter)
}

func validateDiagnosticsTargetSelection(input diagnosticsInput) error {
	hasSingle := strings.TrimSpace(input.FilePath) != ""
	hasBatch := len(input.FilePaths) > 0
	if hasSingle == hasBatch {
		return errors.New("diagnostics requires exactly one of file_path or file_paths")
	}
	if len(collectDiagnosticTargetPaths(fileToolInput{FilePath: input.FilePath, FilePaths: input.FilePaths})) == 0 {
		return errors.New("diagnostics requires at least one non-empty file path")
	}
	return nil
}

// validateDiagnosticsDiffFilter 校验显式差量过滤的模式、路径和一基行号。
func validateDiagnosticsDiffFilter(filter *diagnosticsDiffFilterInput) (*diagnosticsFilterRequest, error) {
	if filter == nil {
		return nil, nil
	}
	if strings.TrimSpace(filter.Mode) != "changed_lines" {
		return nil, fmt.Errorf("diagnostics diff_filter.mode must be %q", "changed_lines")
	}
	if len(filter.ChangedLines) == 0 {
		return nil, errors.New("diagnostics diff_filter.changed_lines must contain at least one file mapping")
	}
	for path, lines := range filter.ChangedLines {
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("diagnostics diff_filter.changed_lines contains an empty file path")
		}
		if err := validateDiagnosticLineNumbers("diff_filter.changed_lines["+path+"]", lines); err != nil {
			return nil, err
		}
	}
	return &diagnosticsFilterRequest{
		mode:               filter.Mode,
		changedLinesByPath: cloneDiagnosticChangedLines(filter.ChangedLines),
	}, nil
}

// validateDiagnosticLineNumbers 确认行号非空且是正的一基值。
func validateDiagnosticLineNumbers(field string, lines []int) error {
	if len(lines) == 0 {
		return fmt.Errorf("diagnostics %s must contain at least one 1-based line number", field)
	}
	for _, line := range lines {
		if line <= 0 {
			return fmt.Errorf("diagnostics %s contains invalid line %d; line numbers are 1-based", field, line)
		}
	}
	return nil
}

// cloneDiagnosticChangedLines 拷贝外部行号映射，避免 handler 持有调用方可变切片。
func cloneDiagnosticChangedLines(input map[string][]int) map[string][]int {
	cloned := make(map[string][]int, len(input))
	for path, lines := range input {
		cloned[path] = append([]int(nil), lines...)
	}
	return cloned
}
