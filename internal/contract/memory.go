package contract

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrFeatureDisabled    = errors.New("feature_disabled")
	ErrMemoryInvalidParam = errors.New("memory invalid params")
	ErrMemoryPersist      = errors.New("memory persist failed")
	ErrMemoryTimedOut     = errors.New("memory timeout")
)

// MemoryScope identifies the ownership boundary used when reading or writing a
// memory entry.
type MemoryScope string

const (
	MemoryScopeUser    MemoryScope = "user"
	MemoryScopeTeam    MemoryScope = "team"
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeLocal   MemoryScope = "local"
)

// ParseMemoryScope 解析记忆作用域。
func ParseMemoryScope(raw string) MemoryScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(MemoryScopeUser):
		return MemoryScopeUser
	case string(MemoryScopeTeam):
		return MemoryScopeTeam
	case string(MemoryScopeProject):
		return MemoryScopeProject
	case string(MemoryScopeLocal):
		return MemoryScopeLocal
	default:
		return MemoryScope("")
	}
}

// Valid 判断跨模块契约是否可用。
func (s MemoryScope) Valid() bool {
	switch s {
	case MemoryScopeUser, MemoryScopeTeam, MemoryScopeProject, MemoryScopeLocal:
		return true
	default:
		return false
	}
}

// MemoryType classifies memory content for provider and retrieval policies.
type MemoryType string

const (
	MemoryTypeUnknown   MemoryType = "unknown"
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

// ParseMemoryType 解析记忆type。
func ParseMemoryType(raw string) MemoryType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user":
		return MemoryTypeUser
	case "feedback":
		return MemoryTypeFeedback
	case "project":
		return MemoryTypeProject
	case "reference":
		return MemoryTypeReference
	case "", "unknown":
		return MemoryTypeUnknown
	default:
		return MemoryTypeUnknown
	}
}

// IsKnown 判断known是否可用。
func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

// MemoryEntry is the normalized memory payload returned across module and tool
// boundaries.
type MemoryEntry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        MemoryType `json:"type"`
	Content     string     `json:"content,omitempty"`
	SourcePath  string     `json:"sourcePath,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// MemoryReadRequest carries the caller identity and lookup selectors for a
// memory read operation.
type MemoryReadRequest struct {
	Name     string
	Path     string
	Scope    MemoryScope
	Type     MemoryType
	AgentID  string
	ThreadID string
	CWD      string
	CallID   string
}

// MemoryReadResult reports the resolved memory entry and degraded/denied state
// without exposing store-specific errors.
type MemoryReadResult struct {
	Entry      *MemoryEntry `json:"entry,omitempty"`
	SourcePath string       `json:"sourcePath,omitempty"`
	IndexHit   bool         `json:"indexHit"`
	DenyReason string       `json:"denyReason,omitempty"`
	Degraded   bool         `json:"degraded,omitempty"`
	Source     string       `json:"source,omitempty"`
}

// MemoryService is the read-only memory port exposed to callers outside the
// memory module.
type MemoryService interface {
	Read(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
}

// AgentMemoryReader is the tool-facing read contract for agent memory.
type AgentMemoryReader interface {
	ReadAgentMemory(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
	MemoryReadEnabled() bool
	MemoryReadToolsEnabled() bool
}

// AgentMemoryWriteRequest carries user-visible content and caller identity for
// an agent memory write.
type AgentMemoryWriteRequest struct {
	Name        string
	Description string
	Content     string
	Type        MemoryType
	Scope       MemoryScope
	Title       string
	AgentID     string
	ThreadID    string
	CWD         string
	CallID      string
	Source      string
}

// AgentMemoryWriteResult reports where a memory write landed and whether it was
// skipped or merged.
type AgentMemoryWriteResult struct {
	Path           string      `json:"path,omitempty"`
	RequestedScope MemoryScope `json:"requestedScope,omitempty"`
	ActualTarget   string      `json:"actualTarget,omitempty"`
	Type           MemoryType  `json:"type,omitempty"`
	Skipped        bool        `json:"skipped,omitempty"`
	Merged         bool        `json:"merged,omitempty"`
}

// AgentMemoryWriter is the tool-facing write contract for agent memory.
type AgentMemoryWriter interface {
	WriteAgentMemory(ctx context.Context, req AgentMemoryWriteRequest) (AgentMemoryWriteResult, error)
	MemoryWriteEnabled() bool
	MemoryWriteToolsEnabled() bool
}

// AgentMemoryError wraps memory tool failures with a stable machine-readable
// code.
type AgentMemoryError struct {
	Code string
	Err  error
}

// Error 返回错误文本。
func (e AgentMemoryError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

// Unwrap 返回底层错误。
func (e AgentMemoryError) Unwrap() error { return e.Err }

// NewAgentMemoryError 创建代理记忆错误。
func NewAgentMemoryError(code string, err error) error {
	return AgentMemoryError{Code: strings.TrimSpace(code), Err: err}
}

// AgentMemoryErrorCode 处理代理记忆错误代码。
func AgentMemoryErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var coded AgentMemoryError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

// ---------------------------------------------------------------------------
// TeamMemoryManager (was team_memory.go)
// ---------------------------------------------------------------------------

// TeamMemoryManager exposes read-only team-memory entrypoints needed by
// sibling memory subpackages during the package split migration.
type TeamMemoryManager interface {
	GetTeamMemPath(buildCtx ...BuildCtx) string
	GetTeamMemEntrypoint(buildCtx ...BuildCtx) string
}
