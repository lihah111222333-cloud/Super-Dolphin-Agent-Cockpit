package intent

import (
	"encoding/json"
	"errors"
	"strings"
)

// Kind 是提示词意图的类型枚举，决定草稿卡片的校验规则和提交路径。
type Kind string

const (
	KindExpert      Kind = "expert"       // 专家能力：有具体执行步骤和输出格式。
	KindRecall      Kind = "recall"       // 知识资料：按主题索引，供模型查阅。
	KindDefaultRule Kind = "default_rule" // 默认规则：作为项目级全局约束。
)

// DraftParams 是创建草稿的请求参数。
type DraftParams struct {
	Kind          string `json:"kind"`
	RawInput      string `json:"raw_input"`
	Cwd           string `json:"cwd,omitempty"`
	SourceType    string `json:"source_type,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	LicenseHint   string `json:"license_hint,omitempty"`
	EnableGlobal  bool   `json:"enable_global,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
}

// DryRunParams 是试问（dry-run）请求参数。
type DryRunParams struct {
	DraftKey string          `json:"draft_key,omitempty"`
	Kind     string          `json:"kind"`
	Card     json.RawMessage `json:"card"`
	Question string          `json:"question"`
	Cwd      string          `json:"cwd,omitempty"`
}

// CommitParams 是提交草稿请求参数。
type CommitParams struct {
	DraftKey      string `json:"draft_key"`
	Cwd           string `json:"cwd,omitempty"`
	ConfirmRisk   bool   `json:"confirm_risk,omitempty"`
	EnableGlobal  bool   `json:"enable_global,omitempty"`
	ConfirmGlobal bool   `json:"confirm_global,omitempty"`
}

// DiscardParams 是丢弃草稿请求参数。
type DiscardParams struct {
	DraftKey string `json:"draft_key"`
	Cwd      string `json:"cwd,omitempty"`
}

// E2EHealthParams 是端到端健康检查请求参数（无字段）。
type E2EHealthParams struct{}

// E2EHealthResult 是端到端健康检查返回结果。
type E2EHealthResult struct {
	Provider        string `json:"provider"`
	FixturePathHash string `json:"fixture_path_hash,omitempty"`
}

// Card 是提示词意图草稿卡片，保存 LLM 生成的结构化内容；不同 kind 使用不同字段子集。
type Card struct {
	Kind                 string         `json:"kind"`
	Title                string         `json:"title"`
	Summary              string         `json:"summary"`
	WhenToUse            string         `json:"when_to_use,omitempty"`
	WhenNotToUse         string         `json:"when_not_to_use,omitempty"`
	Workflow             []string       `json:"workflow,omitempty"`
	Constraints          []string       `json:"constraints,omitempty"`
	Output               string         `json:"output,omitempty"`
	SaveBoundary         string         `json:"save_boundary,omitempty"`
	RecallTopic          string         `json:"recall_topic,omitempty"`
	RecallBody           string         `json:"recall_body,omitempty"`
	DefaultRuleBody      string         `json:"default_rule_body,omitempty"`
	SourceProfile        string         `json:"source_profile,omitempty"`
	SourceFacts          []SourceFact   `json:"source_facts,omitempty"`
	HitExamples          []string       `json:"hit_examples"`
	MissExamples         []string       `json:"miss_examples"`
	ConflictingRules     []RuleConflict `json:"conflicting_rules,omitempty"`
	SuggestedAlternative *Alternative   `json:"suggested_alternative,omitempty"`
}

// Issue 表示草稿校验发现的问题，Severity 为 "block" 时阻止提交，"review" 时需用户确认。
type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// RuleConflict 记录与已有默认规则的冲突信息。
type RuleConflict struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// SourceFact 是从原文提取的单条关键要点，disposition 决定是保留、转写还是丢弃。
type SourceFact struct {
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Disposition string `json:"disposition"`
}

// Alternative 是 LLM 推荐的备选 kind 及原因，当 inferred_kind 与 requested_kind 不同时填入。
type Alternative struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// requireCWD 检查 cwd 非空，否则返回错误；用于所有需要工作目录的 RPC 入口。
func requireCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}
