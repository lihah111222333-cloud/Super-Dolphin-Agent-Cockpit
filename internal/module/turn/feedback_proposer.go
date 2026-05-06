package turn

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

// FeedbackProposer synthesises accumulated feedback into a SKILL.md
// candidate via the dream executor and writes it to the candidate store.
type FeedbackProposer struct {
	dream  contract.DreamExecutor
	store  skillcandidate.Store
	logger *slog.Logger
}

// NewFeedbackProposer constructs a proposer. dream and store must not be nil.
func NewFeedbackProposer(dream contract.DreamExecutor, store skillcandidate.Store, logger *slog.Logger) *FeedbackProposer {
	return &FeedbackProposer{dream: dream, store: store, logger: logger}
}

// Propose collects feedback content, calls the dream executor to synthesise
// a SKILL.md, and inserts it as a pending_review candidate.
func (fp *FeedbackProposer) Propose(ctx context.Context, topicKey string, feedbacks []memory.ExtractedMemory, repoFingerprint string) error {
	if fp.dream == nil || fp.store == nil {
		return fmt.Errorf("feedback proposer not fully configured")
	}

	contents := make([]string, len(feedbacks))
	for i, fb := range feedbacks {
		contents[i] = fb.Content
	}

	prompt := buildFeedbackProposalPrompt(topicKey, contents)

	dreamCtx, cancel := ctxutil.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	skillMD, err := fp.dream.ExecuteDream(dreamCtx, prompt)
	if err != nil {
		return fmt.Errorf("dream execution failed for topic %q: %w", topicKey, err)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(skillMD)))

	sample := skillMD
	if len(sample) > 1024 {
		sample = sample[:1024]
	}

	candidate, err := fp.store.Insert(ctx, skillcandidate.InsertParams{
		Scope:           skillcandidate.ScopeProject,
		Slug:            topicKey,
		ContentHash:     hash,
		RepoFingerprint: repoFingerprint,
		SkillMD:         skillMD,
		RedactedSample:  sample,
	})
	if err != nil {
		return fmt.Errorf("insert candidate for topic %q: %w", topicKey, err)
	}

	if fp.logger != nil {
		fp.logger.Info("feedback skill candidate created",
			"topic", topicKey,
			"candidate_id", candidate.ID,
			"feedback_count", len(feedbacks),
		)
	}
	return nil
}
