package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	timer := time.AfterFunc(d, cancel)
	return ctx, func() {
		timer.Stop()
		cancel()
	}
}

func mustJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func newTurnHandle(localID, providerID string) *turnHandle {
	return &turnHandle{
		localID:    strings.TrimSpace(localID),
		providerID: strings.TrimSpace(providerID),
		done:       make(chan struct{}),
	}
}

func (h *turnHandle) LocalID() string       { return h.localID }
func (h *turnHandle) ProviderID() string    { return h.providerID }
func (h *turnHandle) Done() <-chan struct{} { return h.done }

func (h *turnHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *turnHandle) complete(err error) {
	h.once.Do(func() {
		h.mu.Lock()
		h.err = err
		h.mu.Unlock()
		close(h.done)
	})
}

func cloneCaps(src dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
