// Package difftracker aggregates per-agent tool diffs for UI updates.
//
// The package uses a hybrid strategy: hook-extracted replace_range diffs
// are preferred because they already carry patch text, while code_run and
// every other tool path fall back to git-based diff collection.
//
// Sessions are isolated by agentID (agent_<timestamp>_<hex>) instead of
// threadID so multiple agents on the same thread cannot overwrite each
// other's cumulative diff state.
package difftracker
