package commandcard

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Reader provides read-only access to command cards.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader = contract.CommandCardReader

// ListFilter constrains command-card list queries.
type ListFilter = contract.CommandCardListFilter

// CommandCard is the shared domain DTO for command cards.
type CommandCard = contract.CommandCard
