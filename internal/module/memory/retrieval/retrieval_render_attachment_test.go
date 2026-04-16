package retrieval

import (
	"strings"
	"testing"
	"time"
)

func TestFreezeRelevantMemoryAttachmentsIncludesFreshnessHeader(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	entries := []MemoryEntry{{
		FilePath:  "project/rules.md",
		Content:   "Always run tests before committing.",
		UpdatedAt: now.Add(-72 * time.Hour),
	}}

	attachments := FreezeRelevantMemoryAttachments(entries, now)
	if len(attachments) != 1 {
		t.Fatalf("len(FreezeRelevantMemoryAttachments()) = %d, want 1", len(attachments))
	}
	attachment := attachments[0]
	if !strings.Contains(attachment.Header, "saved 3 days ago") {
		t.Fatalf("attachment header = %q, want freshness warning", attachment.Header)
	}
	if attachment.Path != "project/rules.md" {
		t.Fatalf("attachment path = %q, want project/rules.md", attachment.Path)
	}
}

func TestFreezeRelevantMemoryAttachmentsPreservesFrontmatterMetadata(t *testing.T) {
	now := time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
	entryType := MemoryTypeProject
	attachments := FreezeRelevantMemoryAttachments([]MemoryEntry{{
		Frontmatter: MemoryFrontmatter{
			Name:        "Build Guard",
			Description: "Run guarded build commands",
			Type:        &entryType,
		},
		FilePath:  "project/build-guard.md",
		Content:   "Run ./scripts/go_with_guard.sh build ./...",
		UpdatedAt: now,
	}}, now)
	if len(attachments) != 1 {
		t.Fatalf("len(FreezeRelevantMemoryAttachments()) = %d, want 1", len(attachments))
	}
	content := attachments[0].Content
	for _, want := range []string{
		`name: "Build Guard"`,
		`description: "Run guarded build commands"`,
		`type: "project"`,
		"Run ./scripts/go_with_guard.sh build ./...",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("attachment content missing %q:\n%s", want, content)
		}
	}
}
