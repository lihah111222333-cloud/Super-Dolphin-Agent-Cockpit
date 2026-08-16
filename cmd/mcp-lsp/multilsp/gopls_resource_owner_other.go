//go:build !windows

package multilsp

// goplsRootCohortOwnsResources 保持非 Windows Go 语言服务由共享 daemon
// root cohort controller 独占资源治理与关闭权。
func goplsRootCohortOwnsResources(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "go", "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}
