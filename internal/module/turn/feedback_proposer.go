package turn

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// FeedbackItem is the narrow projection of a memory entry that the
// FeedbackProposer needs. It decouples turn from the memory module's
// concrete ExtractedMemory type; the wiring layer maps between the two.
type FeedbackItem struct {
	Content string
}

// FeedbackProposer is a dormant compatibility shim for the removed legacy
// candidate backend path. It intentionally does not call an LLM or write a
// candidate row in V1.
type FeedbackProposer struct {
	dream  contract.DreamExecutor
	logger *slog.Logger
}

// NewFeedbackProposer keeps the old constructor shape so stale callers compile
// while the live candidate writer remains disabled.
// NewFeedbackProposer 创建feedbackproposer。
func NewFeedbackProposer(dream contract.DreamExecutor, _ any, logger *slog.Logger) *FeedbackProposer {
	return &FeedbackProposer{dream: dream, logger: logger}
}

// Propose no-ops because the live old candidate pipeline is removed.
// Propose 处理propose。
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

// ---------------------------------------------------------------------------
// Feedback proposal prompt builder (was feedback_proposer_prompt.go)
// ---------------------------------------------------------------------------

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
