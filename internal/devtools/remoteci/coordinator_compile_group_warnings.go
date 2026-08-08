package remoteci

import (
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// compileGroupPlanWarnings 提取 compile group 的超目标告警，交给 run warning
// projection；SQLite 会将其与其他 OptimizationWarnings 一起写入 ci_run_warnings。
func compileGroupPlanWarnings(groups []gate.CompileGroup) []string {
	warnings := make([]string, 0)
	for _, group := range groups {
		warning := compileGroupPlanWarning(group)
		if warning == "" {
			continue
		}
		warnings = appendUniqueRemoteWarnings(warnings, []string{warning})
	}
	return warnings
}

// compileGroupPlanWarning 以 group 身份包裹 planner warning，防止多个慢 group
// 因相同数值文本被去重，并使 SQLite warning 可回溯到唯一计划对象。
func compileGroupPlanWarning(group gate.CompileGroup) string {
	warning := strings.TrimSpace(group.BatchPlanWarning)
	if warning == "" {
		return ""
	}
	return fmt.Sprintf("compile_group_plan group_id=%s package_target=%s resource_class_id=%s warning=%s", group.GroupID, group.PackageTarget, group.ResourceClassID, warning)
}
