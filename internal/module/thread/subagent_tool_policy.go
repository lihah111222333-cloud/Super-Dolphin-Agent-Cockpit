package thread

import "strings"

const (
	temporarySubagentTool = "spawn_agent"
	managedSubagentTool   = "orchestration_launch_agent"
)

func persistentSubagentDefaultEnabled(flags map[string]bool) bool {
	if len(flags) == 0 {
		return false
	}
	for name, enabled := range flags {
		if !enabled {
			continue
		}
		switch normalizeSessionFlagName(name) {
		case "persistentsubagentdefault", "managedsubagentdefault", "uipersistentsubagentdefault":
			return true
		}
	}
	return false
}

func applyPersistentSubagentToolPolicy(enabledTools []string, flags map[string]bool) []string {
	if !persistentSubagentDefaultEnabled(flags) || len(enabledTools) == 0 {
		return enabledTools
	}
	hasManaged := false
	hasSpawn := false
	for _, tool := range enabledTools {
		switch strings.TrimSpace(tool) {
		case managedSubagentTool:
			hasManaged = true
		case temporarySubagentTool:
			hasSpawn = true
		}
	}
	if !hasManaged || !hasSpawn {
		return enabledTools
	}
	filtered := make([]string, 0, len(enabledTools)-1)
	for _, tool := range enabledTools {
		if strings.TrimSpace(tool) == temporarySubagentTool {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}
