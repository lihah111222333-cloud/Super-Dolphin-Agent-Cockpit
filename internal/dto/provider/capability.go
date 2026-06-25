package provider

// CapabilitySet 是 provider/thread 支持的能力集合，key 为能力名称，value 表示是否启用。
type CapabilitySet map[string]bool

// 已知能力常量，用于 CapabilitySet 的 key 和协商传参。
const (
	CapMessageSend    = "message_send"    // 支持发送消息。
	CapThreadList     = "thread_list"     // 支持列举 thread。
	CapThreadFork     = "thread_fork"     // 支持 fork thread。
	CapThreadRealtime = "realtime"        // 支持实时推送。
	CapModelSwitch    = "model_switch"    // 支持运行时切换模型。
	CapContextCompact = "context_compact" // 支持上下文压缩。
	CapTurnOverride   = "turn_override"   // 支持 turn 级别参数覆盖。
)

// Has 判断能力集中是否包含指定能力，nil 集合始终返回 false。
func (caps CapabilitySet) Has(cap string) bool {
	if caps == nil {
		return false
	}
	return caps[cap]
}

// All 返回能力集的副本，避免调用方直接修改原始 map；空集合返回空 map 而非 nil。
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
