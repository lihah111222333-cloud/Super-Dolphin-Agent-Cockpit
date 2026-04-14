package memory

import (
	"testing"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestFreezeRelevantMemoryAttachmentsIncludesFreshnessHeader(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	attachments := freezeRelevantMemoryAttachments([]MemoryEntry{{
		FilePath:  "project/commit-style.md",
		Content:   "Use concise imperative commit messages.",
		UpdatedAt: now,
	}}, now)
	if len(attachments) != 1 {
		t.Fatalf("len(freezeRelevantMemoryAttachments()) = %d, want 1", len(attachments))
	}
	attachment := attachments[0]
	if attachment.Kind != dto.AttachmentKindRelevantMemory {
		t.Fatalf("kind = %q, want %q", attachment.Kind, dto.AttachmentKindRelevantMemory)
	}
	if attachment.Path != "project/commit-style.md" {
		t.Fatalf("path = %q, want project/commit-style.md", attachment.Path)
	}
	if attachment.Header != "Memory (saved today): project/commit-style.md:" {
		t.Fatalf("header = %q, want freshness header", attachment.Header)
	}
}
