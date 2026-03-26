package codexapp

import (
	"context"
	"encoding/json"
	"errors"
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

func initializeParams() map[string]any {
	return map[string]any{
		"clientInfo":   map[string]any{"name": "super-agent-v3", "version": "1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
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
func (h *turnHandle) Done() <-chan struct{} { return h.done }

func (h *turnHandle) ProviderID() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.providerID
}

func (h *turnHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *turnHandle) setProviderID(providerID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.providerID = strings.TrimSpace(providerID)
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

func (s *session) configureThread(ctx context.Context, patch dto.ThreadConfigPatch) error {
	threadID := s.ThreadID()
	if threadID == "" {
		return errors.New("codexapp: thread id is required")
	}
	if err := s.applyConfigSet(ctx, threadID, patch); err != nil {
		return err
	}
	if err := s.applyConfigSlashCommands(ctx, threadID, patch); err != nil {
		return err
	}
	s.updateRuntimeConfigFromPatch(patch)
	return nil
}

func (s *session) applyConfigSet(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) error {
	if patch.Model == nil && patch.Effort == nil {
		return nil
	}
	params := map[string]any{"threadId": threadID}
	if patch.Model != nil {
		params["model"] = strings.TrimSpace(*patch.Model)
	}
	if patch.Effort != nil {
		params["effort"] = strings.TrimSpace(*patch.Effort)
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, "thread/config/set", params)
	return err
}

func (s *session) applyConfigSlashCommands(ctx context.Context, threadID string, patch dto.ThreadConfigPatch) error {
	if err := s.applySlashConfig(ctx, threadID, "thread/personality/set", "personality", patch.Personality); err != nil {
		return err
	}
	return s.applySlashConfig(ctx, threadID, "thread/approvals/set", "policy", patch.Approvals)
}

func (s *session) updateRuntimeConfigFromPatch(patch dto.ThreadConfigPatch) {
	if patch.Approvals != nil {
		approval := strings.TrimSpace(*patch.Approvals)
		s.setApprovalPolicy(approval)
		s.setRuntimeConfigValue("approvalPolicy", approval)
		s.setRuntimeConfigValue("approval_policy", approval)
		s.setRuntimeConfigValue("approvals", approval)
	}
	if patch.Personality != nil {
		s.setRuntimeConfigValue("personality", strings.TrimSpace(*patch.Personality))
	}
}

func (s *session) applySlashConfig(ctx context.Context, threadID, method, key string, value *string) error {
	if value == nil {
		return nil
	}
	arg := strings.TrimSpace(*value)
	if arg == "" {
		return nil
	}
	callCtx, cancel := withTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := s.callTransport(callCtx, method, map[string]any{"threadId": threadID, key: arg, "args": arg})
	return err
}

func (s *session) setRuntimeConfigValue(key string, value any) {
	if strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtimeConfig == nil {
		s.runtimeConfig = map[string]any{}
	}
	s.runtimeConfig[key] = value
}

func cloneRuntimeConfigMap(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		out := make(map[string]any, len(cfg))
		for key, value := range cfg {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make(map[string]any, len(cfg))
		for key, value := range cfg {
			out[key] = value
		}
	}
	return out
}
