package buslog

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store reads exception logs from the internal event bus.
type Store = contract.BusLogStore

// ListFilter constrains bus exception log queries.
type ListFilter = contract.BusLogListFilter

// BusExceptionLog is a dashboard projection of one bus exception.
type BusExceptionLog = contract.BusExceptionLog
