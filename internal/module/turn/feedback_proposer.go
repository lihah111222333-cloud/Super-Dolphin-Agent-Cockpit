package turn

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// FeedbackItem 是反馈提炼兼容入口需要的最小记忆投影，避免 turn 包依赖 memory 模块的具体类型。
type FeedbackItem struct {
	Content string
}

// FeedbackProposer 是已停用反馈提炼入口的兼容 shim，只保留旧调用方的构造和方法形状。
type FeedbackProposer struct {
	dream  contract.DreamExecutor
	logger *slog.Logger
}

// NewFeedbackProposer 保留旧构造器签名，当前只返回禁用的兼容 shim。
func NewFeedbackProposer(dream contract.DreamExecutor, _ any, logger *slog.Logger) *FeedbackProposer {
	return &FeedbackProposer{dream: dream, logger: logger}
}

// Propose 只记录禁用日志，不调用 LLM 也不写旧候选结果存储。
func (fp *FeedbackProposer) Propose(ctx context.Context, topicKey string, feedbacks []FeedbackItem, repoFingerprint string) error {
	if fp.logger != nil {
		fp.logger.Info("feedback skill candidate pipeline disabled",
			"topic", topicKey,
			"feedback_count", len(feedbacks),
			"repo_fingerprint", repoFingerprint,
		)
	}
	_ = ctx
	return nil
}

// buildFeedbackProposalPrompt 生成旧反馈聚合提示词；当前保留给兼容路径和测试使用。
func buildFeedbackProposalPrompt(topicKey string, feedbackContents []string) string {
	var sb strings.Builder
	sb.WriteString("你是一个技能提炼专家。以下是用户在多次协作中反复给出的同类反馈：\n\n")
	for i, fb := range feedbackContents {
		fmt.Fprintf(&sb, "反馈 %d:\n%s\n\n", i+1, fb)
	}
	sb.WriteString("请将这些反馈合成为一个 SKILL.md 文件。要求：\n")
	sb.WriteString("1. 以 YAML frontmatter 开头，包含 name 和 description\n")
	sb.WriteString("2. description 以 'Use when' 开头，描述触发条件\n")
	sb.WriteString("3. 正文用 H2 分节，每节一个具体规则\n")
	sb.WriteString("4. 规则要具体可执行，不要泛泛而谈\n")
	sb.WriteString("5. 合并重复内容，保留所有独特要点\n\n")
	sb.WriteString("直接输出 SKILL.md 内容，不要包裹 code fence。")
	return sb.String()
}
