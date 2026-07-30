package codemapindex

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// validateCodemapAbsences 校验手写卷中显式声明的负向路径或符号事实。
func validateCodemapAbsences(root string, doc SemanticMarkdown) []string {
	var problems []string
	for _, lineIndex := range narrativeLineIndexes(doc.Lines) {
		line := doc.Lines[lineIndex]
		matches := codemapAbsentRe.FindAllStringSubmatch(line, -1)
		if strings.Contains(line, "<!--") && strings.Contains(line, "codemap-absent") && len(matches) == 0 {
			problems = append(problems, fmt.Sprintf("%s:%d malformed codemap-absent declaration", doc.File, lineIndex+1))
			continue
		}
		for _, match := range matches {
			if problem := validateCodemapAbsence(root, doc.File, lineIndex+1, match[1]); problem != "" {
				problems = append(problems, problem)
			}
		}
	}
	return problems
}

// validateCodemapAbsence 要求路径、package.Symbol 或 file.go.Symbol 当前确实不存在。
func validateCodemapAbsence(root, codemapFile string, lineNumber int, raw string) string {
	relative := normalizeRepoRelative(raw)
	absolute, err := resolveRepoPath(root, relative)
	if err != nil {
		return fmt.Sprintf("%s:%d invalid codemap-absent path %s: %v", codemapFile, lineNumber, relative, err)
	}
	if _, err := os.Stat(absolute); err == nil {
		ignored, ignoreErr := repositoryPathIgnored(root, relative)
		if ignoreErr != nil {
			return fmt.Sprintf("%s:%d validate ignored codemap path %s: %v", codemapFile, lineNumber, relative, ignoreErr)
		}
		if ignored {
			return ""
		}
		return fmt.Sprintf("%s:%d codemap absence violated: repository path exists: %s", codemapFile, lineNumber, relative)
	} else if !os.IsNotExist(err) {
		return fmt.Sprintf("%s:%d invalid codemap-absent path %s: %v", codemapFile, lineNumber, relative, err)
	}
	symbolPath, symbolInfo, symbols, err := resolveSymbolQualifiedPath(root, relative)
	if err != nil {
		return fmt.Sprintf("%s:%d invalid codemap-absent path %s: %v", codemapFile, lineNumber, relative, err)
	}
	if symbolInfo != nil && repositorySymbolExists(symbolPath, symbolInfo, symbols) {
		return fmt.Sprintf("%s:%d codemap absence violated: repository symbol exists: %s", codemapFile, lineNumber, relative)
	}
	return ""
}

// repositoryPathIgnored 只把当前 Git 工作树明确忽略的本机构建产物排除出仓库路径真相。
func repositoryPathIgnored(root string, relative string) (bool, error) {
	if _, err := os.Lstat(filepath.Join(root, ".git")); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("inspect Git metadata: %w", err)
	}
	command := exec.Command("git", "-C", root, "check-ignore", "--quiet", "--", relative)
	err := command.Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git check-ignore: %w", err)
}
