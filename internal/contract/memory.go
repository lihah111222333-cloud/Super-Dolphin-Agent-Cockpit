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

type MemoryScope string

const (
	MemoryScopeUser    MemoryScope = "user"
	MemoryScopeProject MemoryScope = "project"
	MemoryScopeLocal   MemoryScope = "local"
)

func ParseMemoryScope(raw string) MemoryScope {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(MemoryScopeProject):
		return MemoryScopeProject
	case string(MemoryScopeUser):
		return MemoryScopeUser
	case string(MemoryScopeLocal):
		return MemoryScopeLocal
	default:
		return MemoryScope("")
	}
}

func (s MemoryScope) Valid() bool {
	switch s {
	case MemoryScopeUser, MemoryScopeProject, MemoryScopeLocal:
		return true
	default:
		return false
	}
}

type MemoryType string

const (
	MemoryTypeUnknown   MemoryType = "unknown"
	MemoryTypeUser      MemoryType = "user"
	MemoryTypeFeedback  MemoryType = "feedback"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeReference MemoryType = "reference"
)

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

func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

type MemoryEntry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        MemoryType `json:"type"`
	Content     string     `json:"content,omitempty"`
	SourcePath  string     `json:"sourcePath,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type MemoryReadRequest struct {
	Name  string
	Path  string
	Scope MemoryScope
	Type  MemoryType
}

type MemoryReadResult struct {
	Entry      *MemoryEntry `json:"entry,omitempty"`
	SourcePath string       `json:"sourcePath,omitempty"`
	IndexHit   bool         `json:"indexHit"`
	DenyReason string       `json:"denyReason,omitempty"`
	Degraded   bool         `json:"degraded,omitempty"`
	Source     string       `json:"source,omitempty"`
}

type MemoryWriteRequest struct {
	Name        string
	Description string
	Content     string
	Type        MemoryType
	Scope       MemoryScope
}

type MemoryWriteResult struct {
	Path    string `json:"path"`
	Skipped bool   `json:"skipped"`
}

type MemoryService interface {
	Read(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
	Write(ctx context.Context, req MemoryWriteRequest) (MemoryWriteResult, error)
}
