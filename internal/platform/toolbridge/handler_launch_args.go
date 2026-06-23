package toolbridge

func injectManagedLaunchArgs(args map[string]any, binding toolCallBinding, provider, model, effort string) bool {
	changed := false
	for _, item := range []struct {
		key   string
		value string
	}{
		{key: "parent_id", value: binding.AgentID},
		{key: "provider", value: provider},
		{key: "model", value: model},
		{key: "effort", value: effort},
		{key: "codex_home", value: binding.CodexHome},
		{key: "codex_instance_key", value: binding.CodexInstanceKey},
		{key: "codex_model_provider", value: binding.CodexModelProvider},
	} {
		if setArgStringIfMissing(args, item.key, item.value) {
			changed = true
		}
	}
	return changed
}
