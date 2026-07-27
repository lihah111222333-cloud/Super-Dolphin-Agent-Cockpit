package nodeexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ValidateAutomationCommandWorkspaceScope 使用服务端注入的可信 roots 校验 automation command 执行边界。
// config 自带的 workspace_roots 只能进一步收窄授权，不能自行扩大 ToolScope 允许的路径。
func ValidateAutomationCommandWorkspaceScope(raw json.RawMessage, trustedRoots []string) error {
	if err := ValidateAutomationCommandDispatchConfig(raw); err != nil {
		return err
	}
	cfg, err := ParseAutomationConfig(raw)
	if err != nil {
		return err
	}
	return validateWorkspaceScope(cfg.Exec.CWD, cfg.Exec.WorkspaceRoots, trustedRoots)
}

// validateWorkspaceScope 规范化执行目录与两级根目录，并确认配置只能收窄服务端可信边界。
func validateWorkspaceScope(cwd string, configRoots, trustedRoots []string) error {
	if len(trustedRoots) == 0 {
		return errors.New("trusted workspace roots are required")
	}
	canonicalTrustedRoots, err := canonicalWorkspaceRoots("trusted workspace root", trustedRoots)
	if err != nil {
		return err
	}
	canonicalConfigRoots, err := canonicalWorkspaceRoots("config workspace root", configRoots)
	if err != nil {
		return err
	}
	for index, root := range canonicalConfigRoots {
		if !pathWithinAnyRoot(root, canonicalTrustedRoots) {
			return fmt.Errorf("config workspace root[%d] outside trusted workspace roots", index)
		}
	}
	canonicalCWD, err := canonicalExistingPath("cwd", cwd)
	if err != nil {
		return err
	}
	if !pathWithinAnyRoot(canonicalCWD, canonicalConfigRoots) {
		return errors.New("automation command cwd outside config workspace roots")
	}
	if !pathWithinAnyRoot(canonicalCWD, canonicalTrustedRoots) {
		return errors.New("automation command cwd outside trusted workspace roots")
	}
	return nil
}

func canonicalWorkspaceRoots(label string, roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("%s list is required", label)
	}
	canonical := make([]string, 0, len(roots))
	for index, root := range roots {
		if strings.TrimSpace(root) == "" {
			return nil, fmt.Errorf("%s[%d] is required", label, index)
		}
		resolved, err := canonicalExistingPath(label, root)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", label, index, err)
		}
		canonical = append(canonical, resolved)
	}
	return canonical, nil
}

func pathWithinAnyRoot(pathValue string, roots []string) bool {
	for _, root := range roots {
		if pathWithinRoot(pathValue, root) {
			return true
		}
	}
	return false
}
