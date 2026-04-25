package toolbridge

func injectManagedLaunchArgs(args map[string]any, binding toolCallBinding, provider, model, effort string) bool {
	changed := false
	for _, item := range []struct {
		key   string
		value string
	}{
		{key: "parent_id", value: binding.AgentID},
		{key: "cwd", value: binding.CWD},
		{key: "provider", value: provider},
		{key: "model", value: model},
		{key: "effort", value: effort},
	} {
		if setArgStringIfMissing(args, item.key, item.value) {
			changed = true
		}
	}
	return changed
}
