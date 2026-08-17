package catalog

import (
	"errors"
	"fmt"
	"strings"
)

// CanonicalizeAuthorityFlags 在 Go flag parser 前统一单横线 authority 别名并保留参数值。
func CanonicalizeAuthorityFlags(args []string, names ...string) ([]string, error) {
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimPrefix(name, "--")
		if name == "" || strings.ContainsAny(name, "= \t\r\n") {
			return nil, errors.New("authority flag name is invalid")
		}
		allowed[name] = struct{}{}
	}
	canonical := append([]string(nil), args...)
	for index, argument := range canonical {
		if argument == "--" {
			break
		}
		canonical[index] = canonicalAuthorityAlias(argument, allowed)
	}
	return canonical, nil
}

func canonicalAuthorityAlias(argument string, allowed map[string]struct{}) string {
	if !strings.HasPrefix(argument, "-") || strings.HasPrefix(argument, "--") || argument == "-" {
		return argument
	}
	name, value, hasValue := strings.Cut(strings.TrimPrefix(argument, "-"), "=")
	if _, ok := allowed[name]; !ok {
		return argument
	}
	canonical := "--" + name
	if hasValue {
		canonical += "=" + value
	}
	return canonical
}

// ValidateNoDuplicateAuthorityFlags 在别名规范化后拒绝重复 authority，错误不回显原始值。
func ValidateNoDuplicateAuthorityFlags(args []string, names ...string) error {
	canonical, err := CanonicalizeAuthorityFlags(args, names...)
	if err != nil {
		return err
	}
	allowed := make(map[string]struct{}, len(names))
	for _, name := range names {
		allowed["--"+strings.TrimPrefix(name, "--")] = struct{}{}
	}
	seen := make(map[string]int, len(allowed))
	for index := 0; index < len(canonical); index++ {
		next, stop, err := scanAuthorityOccurrence(canonical, index, allowed, seen)
		if err != nil {
			return err
		}
		if stop {
			break
		}
		index = next
	}
	return nil
}

// scanAuthorityOccurrence 校验一个 canonical 参数并返回已消费的末尾位置。
func scanAuthorityOccurrence(args []string, index int, allowed map[string]struct{}, seen map[string]int) (int, bool, error) {
	argument := args[index]
	if argument == "--" {
		return index, true, nil
	}
	name, _, hasValue := strings.Cut(argument, "=")
	if _, ok := allowed[name]; !ok {
		return index, false, nil
	}
	if previous, ok := seen[name]; ok {
		return index, false, fmt.Errorf("duplicate authority flag %s at argument positions %d and %d", name, previous, index)
	}
	seen[name] = index
	if hasValue || authorityFlagIsBoolean(name) || index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
		return index, false, nil
	}
	return index + 1, false, nil
}

func authorityFlagIsBoolean(name string) bool {
	return name == "--force" || name == "--receipt-only" || name == "--print-plan"
}

// ValidateExplicitTreeFlagArgs 拒绝 Go CLI 中重复或空的 --tree authority。
func ValidateExplicitTreeFlagArgs(args []string) error {
	canonical, err := CanonicalizeAuthorityFlags(args, "tree")
	if err != nil {
		return err
	}
	seen := -1
	for index := 0; index < len(canonical); index++ {
		if canonical[index] == "--" {
			break
		}
		if !isExplicitTreeFlag(canonical[index]) {
			continue
		}
		next, err := validateExplicitTreeOccurrence(canonical, index, seen)
		if err != nil {
			return err
		}
		seen = index
		index = next
	}
	return nil
}

// isExplicitTreeFlag 判断参数是否为 canonical tree flag，而不是相似未知 flag。
func isExplicitTreeFlag(argument string) bool {
	return argument == "--tree" || strings.HasPrefix(argument, "--tree=")
}

// validateExplicitTreeOccurrence 校验一次 tree occurrence 并返回下一个待扫描位置。
func validateExplicitTreeOccurrence(args []string, index, seen int) (int, error) {
	if seen >= 0 {
		return index, fmt.Errorf("duplicate --tree at argument positions %d and %d", seen, index)
	}
	argument := args[index]
	if value, ok := strings.CutPrefix(argument, "--tree="); ok {
		if strings.TrimSpace(value) == "" {
			return index, fmt.Errorf("--tree requires exactly one candidate tree")
		}
		return index, nil
	}
	if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
		return index, fmt.Errorf("--tree requires exactly one candidate tree")
	}
	if isExplicitTreeFlag(args[index+1]) {
		return index, fmt.Errorf("duplicate --tree at argument positions %d and %d", index, index+1)
	}
	if strings.HasPrefix(args[index+1], "--") {
		return index, fmt.Errorf("--tree requires exactly one candidate tree")
	}
	return index + 1, nil
}
