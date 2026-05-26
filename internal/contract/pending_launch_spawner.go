package contract

import (
	"context"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

// PendingLaunchSpawner is the owner-side contract for lazily forking a
// provider CLI when a pending_launch thread receives its first turn.
// thread.Service (internal/module/thread) is the authoritative
// implementation; consumers (internal/module/turn, cmd/* top-level
// wiring, tests) depend on this interface instead of the concrete
// service so cross-module wiring stays provider-neutral.
//
// P22 P4 §2.5 / §281: pre-P4 this interface lived in the turn consumer
// package (internal/module/turn/rpc_helpers.go). That formed a hidden
// side-channel contract — the interface was named and shaped by turn,
// but its semantics belonged to thread.SpawnIfNeeded. Moving the
// interface into internal/contract removes the side-channel, lets the
// compile-time check `var _ contract.PendingLaunchSpawner =
// (thread.Service)(nil)` sit in the owning module, and prevents the
// thread→turn import that the old placement forced.
//
// Contract:
//   - threadID identifies the pending_launch thread.
//   - userInputForRouter is the first-turn user text; the router uses
//     it to evaluate prompt routing before forking the provider CLI.
//   - requestCWD is the cwd supplied by the turn/start caller. It is
//     validation-only: the implementation must launch from the cwd stored
//     on the pending_launch row and reject mismatches before provider side
//     effects.
//   - launched=true means the call actually forked a process; routing
//     is populated only in that case so the UI can show a per-thread
//     routing badge. launched=false is a no-op for already-running
//     threads.
//   - Errors propagate verbatim; nil spawner = C1 path disabled
//     (legacy fail-fast-on-missing-session).
//
// SpawnRouting is defined in the shared internal/dto/thread package so
// neither side of this interface has to import the other.
type PendingLaunchSpawner interface {
	SpawnIfNeeded(ctx context.Context, threadID, userInputForRouter, requestCWD string) (launched bool, routing threaddto.SpawnRouting, err error)
}

type LaunchIntentCompleter interface {
	CompleteLaunchIntent(ctx context.Context, threadID string)
}
