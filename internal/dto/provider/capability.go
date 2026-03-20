package provider

import "fmt"

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

type CapabilityError struct {
	Capability string
	Driver     string
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("capability %q is not supported by %s driver", e.Capability, e.Driver)
}

func NewCapabilityError(cap, driver string) error {
	return &CapabilityError{Capability: cap, Driver: driver}
}

func (c CapabilitySet) Has(cap string) bool {
	if c == nil {
		return false
	}
	return c[cap]
}

func (c CapabilitySet) All(caps ...string) bool {
	for _, cap := range caps {
		if !c.Has(cap) {
			return false
		}
	}
	return true
}
