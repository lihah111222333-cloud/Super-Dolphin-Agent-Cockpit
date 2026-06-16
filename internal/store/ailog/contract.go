package ailog

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store reads normalized AI/runtime logs for dashboards.
type Store = contract.AILogStore

// ListFilter constrains AI log list queries.
type ListFilter = contract.AILogListFilter

// AILog is a normalized log row used by operational dashboards.
type AILog = contract.AILog

// StatusCount summarizes AI logs by status.
type StatusCount = contract.AILogStatusCount
