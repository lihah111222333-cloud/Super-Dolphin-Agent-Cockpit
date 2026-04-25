package toolbridge

import (
	"context"
	"encoding/json"
)

// P22 P4 S3d: narrow consumer interfaces so handler.go / proxy.go no
// longer import internal/store/binding or internal/store/thread
// directly. The concrete store adapters that satisfy these interfaces
// live in module.go (the assembly seam), where platform → store
// imports are legitimate.
//
// Keeping the port definitions in a dedicated file also lets the P4
// S3d archtest scan handler.go / proxy.go for forbidden store imports
// without having to whitelist type declarations.

// agentThreadLookup is the minimum binding-store surface the
// tool-bridge handler needs: given an agentID, tell me the thread it
// is currently bound to. The production bindingstore.Store satisfies
// this structurally because its GetThreadByAgent method already has
// exactly this signature — no adapter required.
type agentThreadLookup interface {
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
}

type toolCallBinding struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	CWD                string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

type toolCallBindingLookup interface {
	GetBindingByAgent(ctx context.Context, agentID string) (toolCallBinding, error)
	GetBindingByProviderThread(ctx context.Context, provider, providerThreadID string) (toolCallBinding, error)
}

// threadConfigOverrideStore is the minimum thread-store surface the
// tool-bridge handler needs: given a threadID, return the raw
// ConfigOverride bytes (or empty + error). The caller unmarshals the
// runtime-only slice from there (see decodeStoredThreadRuntime).
//
// Production threadstore.Store does NOT match this method shape —
// GetByThreadID returns a full *threadstore.Thread. module.go supplies
// a thin adapter (threadConfigOverrideAdapter) that calls GetByThreadID
// and pulls the ConfigOverride field out, so the handler stays ignorant
// of the thread-row type.
type threadConfigOverrideStore interface {
	GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error)
}

// uiPreferenceReader is the minimum UI preference surface needed for
// managed child-agent launch defaults. The production adapter returns the same
// merged global + cwd-scoped view exposed by ui/preferences/getAll.
type uiPreferenceReader interface {
	GetMergedPreferences(ctx context.Context, cwd string) (map[string]any, error)
}
