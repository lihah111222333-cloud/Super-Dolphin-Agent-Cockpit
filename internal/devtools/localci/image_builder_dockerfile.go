package localci

import (
	"fmt"
	"strings"
)

// validateLockedImageArgumentDefaults 确保 Dockerfile 和工具链锁不会形成两个漂移的镜像真值。
func validateLockedImageArgumentDefaults(lines []string, locked map[string]string) error {
	declared := make(map[string]struct{}, len(locked))
	for _, line := range lines {
		name, reachedFrom, err := lockedImageArgumentDefault(line, locked)
		if err != nil {
			return err
		}
		if reachedFrom {
			break
		}
		if name == "" {
			continue
		}
		if _, duplicate := declared[name]; duplicate {
			return fmt.Errorf("Dockerfile ARG %q is declared more than once before FROM", name)
		}
		declared[name] = struct{}{}
	}
	for name := range locked {
		if _, exists := declared[name]; !exists {
			return fmt.Errorf("Dockerfile must declare locked image ARG %q with a default before FROM", name)
		}
	}
	return nil
}

// lockedImageArgumentDefault 解析首个 FROM 之前受锁约束的镜像参数默认值。
func lockedImageArgumentDefault(line string, locked map[string]string) (string, bool, error) {
	instruction, body, found := strings.Cut(line, " ")
	if !found {
		return "", false, nil
	}
	if strings.EqualFold(instruction, "FROM") {
		return "", true, nil
	}
	if !strings.EqualFold(instruction, "ARG") {
		return "", false, nil
	}
	name, value, hasDefault := strings.Cut(strings.TrimSpace(body), "=")
	expected, isLocked := locked[name]
	if !isLocked {
		return "", false, nil
	}
	if !hasDefault || value == "" {
		return "", false, fmt.Errorf("Dockerfile ARG %q must default to its toolchain lock reference", name)
	}
	if value != expected {
		return "", false, fmt.Errorf("Dockerfile ARG %q default does not match the toolchain lock", name)
	}
	return name, false, nil
}
