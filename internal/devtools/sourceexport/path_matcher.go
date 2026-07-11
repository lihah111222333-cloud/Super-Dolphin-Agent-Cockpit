package sourceexport

import (
	"fmt"
	"path"
	"strings"
)

type pathDecision uint8

const (
	pathAllowed pathDecision = iota + 1
	pathDenied
)

// classifyPath 先应用 deny，再应用 allow；未分类路径以稳定错误码失败。
func classifyPath(policy Policy, filePath string) (pathDecision, error) {
	if err := validatePolicyPath("path", filePath); err != nil {
		return 0, err
	}
	matched, err := matchesAnyRule(policy.DenyRules, filePath)
	if err != nil {
		return 0, err
	}
	if matched {
		return pathDenied, nil
	}
	matched, err = matchesAnyRule(policy.AllowRules, filePath)
	if err != nil {
		return 0, err
	}
	if matched {
		return pathAllowed, nil
	}
	return 0, &Error{Code: CodeUnclassifiedPath, Path: filePath, Err: fmt.Errorf("path does not match an allow or deny rule")}
}

func matchesAnyRule(rules []PathRule, filePath string) (bool, error) {
	for _, rule := range rules {
		matched, err := matchPathRule(rule, filePath)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

// matchPathRule 支持精确文件和按路径段解释的 doublestar glob。
func matchPathRule(rule PathRule, filePath string) (bool, error) {
	if rule.Kind == "file" {
		return rule.Pattern == filePath, nil
	}
	if rule.Kind != "glob" {
		return false, policyError("rule.kind", fmt.Errorf("unsupported kind %q", rule.Kind))
	}
	return matchGlobSegments(strings.Split(rule.Pattern, "/"), strings.Split(filePath, "/"), 0, 0)
}

// matchGlobSegments 递归匹配路径段；`**` 可以消费零个或多个完整路径段。
func matchGlobSegments(pattern []string, value []string, patternIndex int, valueIndex int) (bool, error) {
	if patternIndex == len(pattern) {
		return valueIndex == len(value), nil
	}
	if pattern[patternIndex] == "**" {
		return matchDoubleStar(pattern, value, patternIndex, valueIndex)
	}
	if valueIndex == len(value) {
		return false, nil
	}
	matched, err := path.Match(pattern[patternIndex], value[valueIndex])
	if err != nil || !matched {
		return false, err
	}
	return matchGlobSegments(pattern, value, patternIndex+1, valueIndex+1)
}

func matchDoubleStar(pattern []string, value []string, patternIndex int, valueIndex int) (bool, error) {
	for nextValueIndex := valueIndex; nextValueIndex <= len(value); nextValueIndex++ {
		matched, err := matchGlobSegments(pattern, value, patternIndex+1, nextValueIndex)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func isForbiddenFileName(policy Policy, filePath string) bool {
	baseName := path.Base(filePath)
	for _, pattern := range policy.ForbiddenFileNames {
		matched, err := path.Match(pattern, baseName)
		if err == nil && matched {
			return true
		}
	}
	return false
}
