// Package observation owns the Canonical Turn Observation implementation for
// the P21 plans (P0b / P3). It normalizes the raw + typed event streams into
// six facts that downstream consumers read.
//
// Pure data types and interfaces now live in internal/dto/observation so that
// consumers (insight, dashboard, extractors) can depend on them without
// importing the module/turn subtree. This package re-exports every public
// symbol from the dto package as type aliases so existing callers inside the
// turn module continue to compile without changes.
package observation

import (
	dtoobs "github.com/anthropic-ai/super-agent-v3/internal/dto/observation"
)

// ─── type aliases ────────────────────────────────────────────────────────────
// Every public type and constant defined in dto/observation is re-exported
// here so that existing in-module callers (memory.go, subscribers.go, etc.)
// and tests keep working without import changes.

type TerminalKind = dtoobs.TerminalKind

const (
	TerminalUnknown     = dtoobs.TerminalUnknown
	TerminalCompleted   = dtoobs.TerminalCompleted
	TerminalStalled     = dtoobs.TerminalStalled
	TerminalFailed      = dtoobs.TerminalFailed
	TerminalInterrupted = dtoobs.TerminalInterrupted
	TerminalAborted     = dtoobs.TerminalAborted
)

type Terminal = dtoobs.Terminal
type TokenSnapshot = dtoobs.TokenSnapshot
type DedupeKey = dtoobs.DedupeKey
type Counts = dtoobs.Counts
type Timestamps = dtoobs.Timestamps

type ObservationReader = dtoobs.ObservationReader
type ObservationWriter = dtoobs.ObservationWriter
type Contract = dtoobs.Contract

// ─── implementation helpers ─────────────────────────────────────────────────

// terminalPrecedence 返回 RecordTerminal 使用的优先级顺序，数值越大越优先。
// Interrupted/Aborted 为粘性种类，即便同优先级也不会被覆盖。
func terminalPrecedence(k TerminalKind) int {
	switch k {
	case TerminalInterrupted, TerminalAborted:
		return 5
	case TerminalFailed:
		return 4
	case TerminalStalled:
		return 3
	case TerminalCompleted:
		return 2
	default:
		return 0
	}
}
