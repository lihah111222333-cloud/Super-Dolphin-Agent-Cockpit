package sharedfile

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Reader provides read-only access to shared files.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader = contract.SharedFileReader

// Upserter writes or overwrites a shared file by path.
type Upserter = contract.SharedFileUpserter

// Deleter removes a shared file by path. Returns the number of rows deleted.
type Deleter = contract.SharedFileDeleter

// Store combines read and mutation access to shared files.
type Store = contract.SharedFileStore

// UpsertParams drives Store.Upsert.
type UpsertParams = contract.SharedFileUpsertParams

// ListFilter constrains shared-file list queries.
type ListFilter = contract.SharedFileListFilter

// SharedFile is the shared domain DTO for shared files.
type SharedFile = contract.SharedFile
