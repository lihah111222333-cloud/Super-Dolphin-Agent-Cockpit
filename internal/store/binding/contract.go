package binding

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store persists agent/thread/provider bindings.
type Store = contract.BindingStore

// RebindParams moves an agent binding to a new thread/cwd tuple.
type RebindParams = contract.BindingRebindParams

// UpsertParams inserts or repairs a binding row.
type UpsertParams = contract.BindingUpsertParams

// UpdateSessionUUIDParams updates the session UUID for an agent.
type UpdateSessionUUIDParams = contract.BindingUpdateSessionUUIDParams

// UpdateProviderThreadIDParams updates the provider thread id.
type UpdateProviderThreadIDParams = contract.BindingUpdateProviderThreadIDParams

// SetArchivedParams archives or unarchives an agent binding.
type SetArchivedParams = contract.BindingSetArchivedParams

// BindAgentThreadParams binds an agent id to a public thread id.
type BindAgentThreadParams = contract.BindingBindAgentThreadParams

// UpdateAgentCwdParams updates the persisted cwd for an agent binding.
type UpdateAgentCwdParams = contract.BindingUpdateAgentCwdParams

// Binding is the binding row projection.
type Binding = contract.Binding
