package toolbridge

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const legacyOrchSurfacePrefix = "orchestration_"

// OrchestrationToolAliasDenylist 返回 orchestration 控制面的 canonical 和 legacy peer 名。
func OrchestrationToolAliasDenylist() []string {
	return contract.OrchestrationToolAliasDenylist()
}

// legacyLSPName 返回 LSP 工具短名对应的旧版 lsp_* 名称，供兼容 surface 暴露。
func legacyLSPName(canonical string) string {
	for legacy, short := range legacyLSPToolAliases {
		if short == canonical {
			return legacy
		}
	}
	return ""
}

// legacyOrchPeerRealName 返回 orchestration 短名对应的旧 peer realName。
func legacyOrchPeerRealName(canonical string) string {
	canonical = strings.TrimSpace(canonical)
	for _, alias := range contract.OrchestrationToolAliases() {
		if alias.Canonical == canonical {
			return alias.LegacyPeerRealName
		}
	}
	return ""
}

func legacyManagedLaunchPeerRealName() string {
	return legacyOrchPeerRealName("launch_agent")
}

func canonicalOrchSurfaceName(name string) string {
	name = strings.TrimSpace(name)
	for _, alias := range contract.OrchestrationToolAliases() {
		if alias.LegacyPeerRealName == name {
			return alias.Canonical
		}
	}
	return name
}

func isLegacyOrchPeerRealName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && canonicalOrchSurfaceName(name) != name
}

func requiresLegacyOrchSurfaceName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), legacyOrchSurfacePrefix)
}
