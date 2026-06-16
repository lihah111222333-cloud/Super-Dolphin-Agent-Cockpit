package systemlog

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store reads and writes system log rows.
type Store = contract.SystemLogStore

// ListFilter constrains system log list queries.
type ListFilter = contract.SystemLogListFilter

// InsertParams captures one raw system log write.
type InsertParams = contract.SystemLogInsertParams

// SystemLog is a normalized system log row.
type SystemLog = contract.SystemLog
