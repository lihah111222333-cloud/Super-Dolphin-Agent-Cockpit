package provider

type CapabilitySet map[string]bool

const (
	CapMessageSend    = "message_send"
	CapThreadList     = "thread_list"
	CapThreadFork     = "thread_fork"
	CapThreadRealtime = "realtime"
	CapModelSwitch    = "model_switch"
	CapContextCompact = "context_compact"
	CapTurnOverride   = "turn_override"
)

// Has 判断DTO是否可用。
func (caps CapabilitySet) Has(cap string) bool {
	if caps == nil {
		return false
	}
	return caps[cap]
}

// All 处理all。
func (caps CapabilitySet) All() map[string]bool {
	if len(caps) == 0 {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(caps))
	for key, value := range caps {
		out[key] = value
	}
	return out
}
