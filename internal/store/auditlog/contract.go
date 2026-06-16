package auditlog

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Store reads and writes audit events.
type Store = contract.AuditLogStore

// ListFilter constrains audit event list queries.
type ListFilter = contract.AuditLogListFilter

// InsertParams captures one audit event write.
type InsertParams = contract.AuditLogInsertParams

// AuditEvent is a normalized audit event row.
type AuditEvent = contract.AuditEvent
