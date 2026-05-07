// Package classifier picks a prompt_template for a new conversation's first
// user input.
//
// Why it exists:
//   - SystemPromptPage lets the user pin exactly one "launch prompt"; the
//     router's prior keyword-routing layer was removed because it conflicted
//     with upstream CLIs' own intent handling (see router_resolve.go).
//   - For users who want "smart auto-pick across the whole library" without
//     the old keyword-match footgun, a small LLM call is the cleanest handoff:
//     deterministic-enough at top-1 granularity, cheaper than duplicating
//     intent classification in Go, and short-circuits cleanly when the user
//     has already pinned a prompt (the pin wins, classifier is skipped).
//
// The canonical interface and DTO types are defined in internal/contract
// (PromptClassifier*) so that consumers (thread module) depend only on the
// contract layer. This package provides type aliases for backward
// compatibility and houses the concrete implementations.
package classifier

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Candidate is an alias for the contract-layer DTO.
type Candidate = contract.PromptClassifierCandidate

// Input is an alias for the contract-layer DTO.
type Input = contract.PromptClassifierInput

// Result is an alias for the contract-layer DTO.
type Result = contract.PromptClassifierResult

// FastPathDecision is an alias for the contract-layer DTO.
type FastPathDecision = contract.PromptClassifierFastPathDecision

// Classifier is an alias for the contract-layer interface.
type Classifier = contract.PromptClassifier
