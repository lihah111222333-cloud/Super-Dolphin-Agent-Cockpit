//go:build windows

package multilsp

// goplsRootCohortOwnsResources 表示 Windows shared broker/root Job 是 gopls 资源的唯一 owner。
func goplsRootCohortOwnsResources(languageID string) bool {
	switch normalizeLanguageID(languageID) {
	case "go", "gomod", "gosum", "gowork":
		return true
	default:
		return false
	}
}
