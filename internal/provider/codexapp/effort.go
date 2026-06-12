package codexapp

import "strings"

func normalizeCodexAppEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal":
		return "low"
	default:
		return strings.TrimSpace(effort)
	}
}
