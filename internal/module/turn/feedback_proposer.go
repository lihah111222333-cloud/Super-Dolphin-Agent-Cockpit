package turn

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
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
	logger *pkglogger.Logger
}

// NewFeedbackProposer keeps the old constructor shape so stale callers compile
// while the live candidate writer remains disabled.
// NewFeedbackProposer 创建feedbackproposer。
func NewFeedbackProposer(dream contract.DreamExecutor, _ any, logger *pkglogger.Logger) *FeedbackProposer {
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
