package turn

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/skillcandidate"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

// FeedbackItem is the narrow projection of a memory entry that the
// FeedbackProposer needs. It decouples turn from the memory module's
// concrete ExtractedMemory type; the wiring layer maps between the two.
type FeedbackItem struct {
	Content string
}

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
func (fp *FeedbackProposer) Propose(ctx context.Context, topicKey string, feedbacks []FeedbackItem, repoFingerprint string) error {
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
