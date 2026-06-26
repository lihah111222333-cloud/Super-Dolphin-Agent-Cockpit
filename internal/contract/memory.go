package contract

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// 记忆模块跨层错误哨兵，供 handler/toolbridge 用 errors.Is 做稳定分类。
	ErrFeatureDisabled    = errors.New("feature_disabled")
	ErrMemoryInvalidParam = errors.New("memory invalid params")
	ErrMemoryPersist      = errors.New("memory persist failed")
	ErrMemoryTimedOut     = errors.New("memory timeout")
)

// MemoryScope 是记忆工具 wire 参数中的作用域枚举。
// 服务层用它决定读写落点和权限过滤，未知值必须在进入持久化前被拒绝。
type MemoryScope string

const (
	// MemoryScopeUser 指向当前用户的长期记忆。
	MemoryScopeUser MemoryScope = "user"
	// MemoryScopeTeam 指向团队共享记忆。
	MemoryScopeTeam MemoryScope = "team"
	// MemoryScopeProject 指向当前项目记忆。
	MemoryScopeProject MemoryScope = "project"
	// MemoryScopeLocal 指向当前工作区本地记忆。
	MemoryScopeLocal MemoryScope = "local"
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

// Valid 校验 MemoryScope 是否属于当前 contract 支持的 wire 值。
func (s MemoryScope) Valid() bool {
	switch s {
	case MemoryScopeUser, MemoryScopeTeam, MemoryScopeProject, MemoryScopeLocal:
		return true
	default:
		return false
	}
}

// MemoryType 是记忆条目的 wire 分类，用于索引过滤和写入归档。
type MemoryType string

const (
	// MemoryTypeUnknown 承载调用方未提供或无法识别的记忆类型。
	MemoryTypeUnknown MemoryType = "unknown"
	// MemoryTypeUser 归类用户偏好或长期资料类记忆。
	MemoryTypeUser MemoryType = "user"
	// MemoryTypeFeedback 归类反馈沉淀类记忆。
	MemoryTypeFeedback MemoryType = "feedback"
	// MemoryTypeProject 归类项目上下文类记忆。
	MemoryTypeProject MemoryType = "project"
	// MemoryTypeReference 归类可引用资料类记忆。
	MemoryTypeReference MemoryType = "reference"
)

// ParseMemoryType 解析记忆类型。
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

// IsKnown 校验 MemoryType 是否为可写入、可检索的业务类型。
// unknown 只用于宽松解析和展示，不能作为有效业务分类参与过滤。
func (t MemoryType) IsKnown() bool {
	switch ParseMemoryType(string(t)) {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeProject, MemoryTypeReference:
		return true
	default:
		return false
	}
}

// MemoryEntry 是记忆服务返回给工具和 prompt 模块的只读条目视图。
// SourcePath 只用于解释来源，不代表调用方可以绕过服务权限直接读写文件。
type MemoryEntry struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        MemoryType `json:"type"`
	Content     string     `json:"content,omitempty"`
	SourcePath  string     `json:"sourcePath,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// MemoryReadRequest 承载记忆读取的 wire 入参。
// Name/Path 用于定位条目，Scope/Type 用于权限和索引过滤，AgentID/ThreadID/CWD/CallID 供审计与降级判断使用。
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

// MemoryReadResult 是记忆读取返回给工具层的稳定 wire 形状。
// DenyReason 和 Degraded 让工具层能明确展示权限拒绝或降级，而不是静默返回空结果。
type MemoryReadResult struct {
	Entry      *MemoryEntry `json:"entry,omitempty"`
	SourcePath string       `json:"sourcePath,omitempty"`
	IndexHit   bool         `json:"indexHit"`
	DenyReason string       `json:"denyReason,omitempty"`
	Degraded   bool         `json:"degraded,omitempty"`
	Source     string       `json:"source,omitempty"`
}

// MemoryService 是 prompt、toolbridge 与 memory 模块之间的只读 contract 边界。
type MemoryService interface {
	Read(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
}

// AgentMemoryReader 是 agent runtime 暴露给工具调用面的记忆读取能力。
// Enabled 方法分别区分模块能力开关和工具面是否允许展示。
type AgentMemoryReader interface {
	ReadAgentMemory(ctx context.Context, req MemoryReadRequest) (MemoryReadResult, error)
	MemoryReadEnabled() bool
	MemoryReadToolsEnabled() bool
}

// AgentMemoryWriteRequest 承载 agent 写入记忆时的内容、归属和审计来源。
// Scope 是请求目标，实际落点仍由服务按权限和工作区上下文裁决。
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

// AgentMemoryWriteResult 返回记忆写入后的实际落点和处理结果。
// Skipped/Merged 用于区分未写入、合并更新和新建，避免调用方把空路径当成功。
type AgentMemoryWriteResult struct {
	Path           string      `json:"path,omitempty"`
	RequestedScope MemoryScope `json:"requestedScope,omitempty"`
	ActualTarget   string      `json:"actualTarget,omitempty"`
	Type           MemoryType  `json:"type,omitempty"`
	Skipped        bool        `json:"skipped,omitempty"`
	Merged         bool        `json:"merged,omitempty"`
}

// AgentMemoryWriter 是 agent runtime 暴露给工具调用面的记忆写入能力。
type AgentMemoryWriter interface {
	WriteAgentMemory(ctx context.Context, req AgentMemoryWriteRequest) (AgentMemoryWriteResult, error)
	MemoryWriteEnabled() bool
	MemoryWriteToolsEnabled() bool
}

// AgentMemoryError 携带稳定 code 和底层错误，供 UI 与工具层做可预测展示。
type AgentMemoryError struct {
	Code string
	Err  error
}

// Error 返回可展示的错误文本，优先透传底层错误以保留存储或权限失败原因。
func (e AgentMemoryError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

// Unwrap 暴露底层错误，允许调用方用 errors.Is/As 做稳定分类。
func (e AgentMemoryError) Unwrap() error { return e.Err }

// NewAgentMemoryError 创建带稳定 code 的代理记忆错误。
func NewAgentMemoryError(code string, err error) error {
	return AgentMemoryError{Code: strings.TrimSpace(code), Err: err}
}

// AgentMemoryErrorCode 提取代理记忆错误代码。
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

// TeamMemoryManager 暴露 team-memory 路径和入口的只读 contract 边界。
// memory 子模块通过该接口读取共享位置，避免跨包直接依赖具体 manager 实现。
type TeamMemoryManager interface {
	GetTeamMemPath(buildCtx ...BuildCtx) string
	GetTeamMemEntrypoint(buildCtx ...BuildCtx) string
}
