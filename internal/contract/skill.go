package contract

import (
	"context"
	"strings"
)

// TrustScope 分组定义 skill 的信任边界。

// TrustScope 描述 skill 的信任范围，影响自动调用权限、审批策略和默认工具白名单。
// user/signed 可按本地可信处理；project 来自项目目录，首次使用时必须保留审批边界。
type TrustScope string

// skill 信任范围常量。
const (
	TrustUnknown TrustScope = ""
	TrustUser    TrustScope = "user"
	TrustProject TrustScope = "project"
	TrustSigned  TrustScope = "signed"
)

// Valid 判断信任范围是否为系统认识的 tier。
func (t TrustScope) Valid() bool {
	switch t {
	case TrustUser, TrustProject, TrustSigned:
		return true
	}
	return false
}

// Trusted 判断该信任范围是否可跳过逐次调用审批。
// project 始终返回 false，避免项目仓库内 skill 自动获得用户级权限。
func (t TrustScope) Trusted() bool {
	return t == TrustUser || t == TrustSigned
}

// SkillInfo 分组定义只读 skill 元数据。

// SkillInfo 是单个 skill 的只读元数据投影。
// dashboard、目录兼容层和 provider mirror 只读取这些字段，不通过 DTO 修改 canonical skill。
type SkillInfo struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name,omitempty"`
	Dir          string   `json:"dir"`
	Scope        string   `json:"scope,omitempty"`
	PersonalType string   `json:"personal_type,omitempty"`
	Description  string   `json:"description"`
	Summary      string   `json:"summary"`
	TriggerWords []string `json:"trigger_words,omitempty"`
	ForceWords   []string `json:"force_words,omitempty"`
	// Trust 是 skill 的信任范围："user" / "project" / "signed"。
	Trust TrustScope `json:"trust,omitempty"`
	// AllowedTools 是 skill 级工具白名单；为空表示继承当前会话默认工具策略。
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisableModelInvocation 为 true 时不暴露给模型自动调用，只允许用户显式命令触发。
	DisableModelInvocation bool `json:"disable_model_invocation,omitempty"`
	// ContentHash 是 SKILL.md 正文 SHA-256，用于内容变化后强制重新审批。
	ContentHash string `json:"content_hash,omitempty"`
	// ReplacesNative 声明该 skill 在语义上替代的 provider 原生工具 ID。
	ReplacesNative map[string][]string `json:"replaces_native,omitempty"`
}

// SkillLister 分组定义 skill 元数据读取端口。

// SkillLister 是面向 dashboard 和兼容消费者的只读 skill 元数据端口。
type SkillLister interface {
	ListSkills(ctx context.Context) ([]SkillInfo, error)
}

// SkillInventoryLister 是管理页读取 skill inventory 的端口。
// 它返回 canonical 原始记录，让重名和策略隐藏来源仍可用于冲突处理。
type SkillInventoryLister interface {
	ListSkillInventory(ctx context.Context) ([]SkillInfo, error)
}

// Skill CWD 上下文 helper 分组定义 skill 请求的工作目录作用域。

// SkillCWDContextKey 是 skill 请求工作目录的 context key。
// 导出该 key 是为了让 contract helper 与 skill 模块内部读取逻辑共享同一作用域。
type SkillCWDContextKey struct{}

// WithSkillCWD 在 context 中附加 skill 请求的工作目录。
// 空 cwd 不写入 context，调用方仍可用 RequireSkillCWD 明确失败。
func WithSkillCWD(ctx context.Context, cwd string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ctx
	}
	return context.WithValue(ctx, SkillCWDContextKey{}, cwd)
}

// SkillCWDFromContext 从 context 读取 skill 工作目录，缺失时返回空字符串。
func SkillCWDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(SkillCWDContextKey{}).(string)
	return strings.TrimSpace(value)
}

// RequireSkillCWD 读取 skill 工作目录；缺失时返回 ErrSkillMissingCWD。
func RequireSkillCWD(ctx context.Context) (string, error) {
	cwd := SkillCWDFromContext(ctx)
	if cwd == "" {
		return "", ErrSkillMissingCWD
	}
	return cwd, nil
}

// ArtifactKind 分组定义 skill 内容产物分类。

// ArtifactKind 常量用于区分 skill 元数据、正文和资源文件。
// 审批缓存和 turn expanded-state 去重会按这些分类控制粒度。
const (
	// ArtifactKindMetadata 表示 skill 元数据，如名称、描述和摘要。
	ArtifactKindMetadata = "metadata"
	// ArtifactKindBody 表示 SKILL.md 正文或其锚点片段。
	ArtifactKindBody = "body"
	// ArtifactKindResource 表示 skill 目录内的 references、scripts、assets 等资源文件。
	ArtifactKindResource = "resource"
)

// IsValidArtifactKind 判断 kind 是否属于已知 skill 产物分类。
func IsValidArtifactKind(kind string) bool {
	switch kind {
	case ArtifactKindMetadata, ArtifactKindBody, ArtifactKindResource:
		return true
	}
	return false
}

// SkillHydrationSource 分组定义 turn service 对 skill 的最小依赖。

// SkillHydrationSource 在提交 provider 前解析只含名称的 skill 引用。
// 该端口只暴露列表和本地读取能力，避免 turn 模块直接依赖 skill 服务实现。
type SkillHydrationSource interface {
	SkillLister
	ReadLocal(ctx context.Context, path string) (any, error)
}

// Skill mirror provider 切换契约分组定义 provider mirror 同步结果。

// SkillProvider 标识接收 skill mirror 的 provider 类型。
type SkillProvider string

// skill mirror 支持的 provider 常量。
const (
	SkillProviderClaude SkillProvider = "claude"
	SkillProviderCodex  SkillProvider = "codex"
)

// SkillMirrorReport 汇总一次 provider mirror 发布、跳过、删除和冲突结果。
type SkillMirrorReport struct {
	Published []SkillMirrorReportItem `json:"published,omitempty"`
	Skipped   []SkillMirrorReportItem `json:"skipped,omitempty"`
	Deleted   []SkillMirrorReportItem `json:"deleted,omitempty"`
	Conflicts []SkillMirrorReportItem `json:"conflicts,omitempty"`
}

// SkillMirrorReportItem 记录单个 mirror 目标的同步结果和冲突原因。
type SkillMirrorReportItem struct {
	TargetID           string        `json:"target_id"`
	Provider           SkillProvider `json:"provider,omitempty"`
	Scope              string        `json:"scope,omitempty"`
	RelativeMirrorPath string        `json:"relative_mirror_path,omitempty"`
	CanonicalID        string        `json:"canonical_id,omitempty"`
	OldHash            string        `json:"old_hash,omitempty"`
	NewHash            string        `json:"new_hash,omitempty"`
	ConflictKind       string        `json:"conflict_kind,omitempty"`
	Error              string        `json:"error,omitempty"`
}

// SkillProviderMirrorTarget 描述一个 provider skill mirror 的目标目录。
type SkillProviderMirrorTarget struct {
	Provider          string `json:"provider"`
	HomeRoot          string `json:"home_root"`
	SkillsRoot        string `json:"skills_root"`
	AllowExplicitHome bool   `json:"allow_explicit_home,omitempty"`
}

// SkillMirrorReconciler 是 provider mirror 生成器的跨模块端口。
type SkillMirrorReconciler interface {
	ReconcileProviderMirrors(ctx context.Context, cwd string, targets []SkillProviderMirrorTarget) (SkillMirrorReport, error)
}
