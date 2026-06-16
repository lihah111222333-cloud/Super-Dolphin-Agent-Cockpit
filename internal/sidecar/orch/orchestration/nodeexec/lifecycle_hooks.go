package nodeexec

func cloneHookHandlers(hooks map[HookPoint]HookHandler) map[HookPoint]HookHandler {
	if len(hooks) == 0 {
		return nil
	}
	out := make(map[HookPoint]HookHandler, len(hooks))
	for point, handler := range hooks {
		if handler != nil {
			out[point] = handler
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
