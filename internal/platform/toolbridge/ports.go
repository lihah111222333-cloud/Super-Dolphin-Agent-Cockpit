package toolbridge

import (
	"context"
	"encoding/json"
)

// P22 P4 S3d: narrow consumer interfaces so handler.go / proxy.go no
// longer import internal/store/binding or internal/store/thread
// directly. The concrete store adapters that satisfy these interfaces
// live in internal/app/toolbridge_adapters.go (the assembly seam).
//
// Keeping the port definitions in a dedicated file also lets the P4
// S3d archtest scan handler.go / proxy.go for forbidden store imports
// without having to whitelist type declarations.

// AgentThreadLookup is the minimum binding-store surface the
// tool-bridge handler needs: given an agentID, tell me the thread it
// is currently bound to.
type AgentThreadLookup interface {
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
}

// agentThreadLookup is the internal alias used by Handler fields.
type agentThreadLookup = AgentThreadLookup

// ToolCallBinding is the projection of a binding store row that the
// toolbridge handler needs for managed-launch context injection.
type ToolCallBinding struct {
	AgentID            string
	Provider           string
	ProviderThreadID   string
	CodexThreadID      string
	CWD                string
	ParentAgentID      string
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// toolCallBinding is the internal alias used throughout toolbridge.
type toolCallBinding = ToolCallBinding

// ToolCallBindingLookup extends AgentThreadLookup with binding-level
// resolution methods.
type ToolCallBindingLookup interface {
	GetBindingByAgent(ctx context.Context, agentID string) (ToolCallBinding, error)
	GetBindingByProviderThread(ctx context.Context, provider, providerThreadID string) (ToolCallBinding, error)
}

type toolCallBindingLookup = ToolCallBindingLookup

// ThreadConfigOverrideStore is the minimum thread-store surface the
// tool-bridge handler needs: given a threadID, return the raw
// ConfigOverride bytes (or empty + error). The caller unmarshals the
// runtime-only slice from there (see decodeStoredThreadRuntime).
type ThreadConfigOverrideStore interface {
	GetConfigOverride(ctx context.Context, threadID string) (json.RawMessage, error)
}

type threadConfigOverrideStore = ThreadConfigOverrideStore

// UIPreferenceReader is the minimum UI preference surface needed for
// managed child-agent launch defaults. The production adapter returns the same
// merged global + cwd-scoped view exposed by ui/preferences/getAll.
type UIPreferenceReader interface {
	GetMergedPreferences(ctx context.Context, cwd string) (map[string]any, error)
}

type uiPreferenceReader = UIPreferenceReader
