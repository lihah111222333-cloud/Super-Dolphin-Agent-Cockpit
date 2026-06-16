package thread

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store is kept as a compatibility alias for the thread persistence port.
type Store = contract.ThreadStore

// UpsertParams is kept as a compatibility alias for thread upsert data.
type UpsertParams = contract.ThreadUpsertParams

// UpdateStatusParams is kept as a compatibility alias for thread status updates.
type UpdateStatusParams = contract.ThreadUpdateStatusParams

// UpdateLaunchResultParams is kept as a compatibility alias for launch result updates.
type UpdateLaunchResultParams = contract.ThreadUpdateLaunchResultParams

// ExpireStaleParams is kept as a compatibility alias for stale-thread expiration data.
type ExpireStaleParams = contract.ThreadExpireStaleParams

// Thread is kept as a compatibility alias for the persisted thread read model.
type Thread = contract.Thread

// PromptSnapshot is kept as a compatibility alias for persisted prompt snapshots.
type PromptSnapshot = contract.PromptSnapshot

// PromptBoundary is kept as a compatibility alias for prompt snapshot boundaries.
type PromptBoundary = contract.PromptBoundary

// RunningAgent is kept as a compatibility alias for live running-agent rows.
type RunningAgent = contract.RunningAgent

// ThreadCwd is kept as a compatibility alias for thread cwd projections.
type ThreadCwd = contract.ThreadCwd
