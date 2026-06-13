package thread

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/thread/titleextract"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// ExtractTitle 提取title。
func ExtractTitle(prompt string) string {
	return titleextract.Extract(prompt)
}

func countDisplayUnits(s string) int {
	return titleextract.CountDisplayUnits(s)
}

func resolveDisplayName(ctx context.Context, store threadstore.Store, agentID, _ string, currentName string) string {
	name := strings.TrimSpace(currentName)
	if name == defaultThreadName() {
		name = ""
	}
	if store != nil {
		existing, err := store.GetByThreadID(ctx, agentID)
		if err == nil && existing.ManuallyRenamed {
			return strings.TrimSpace(existing.Name)
		}
	}
	return name
}

func defaultThreadName() string {
	return "新对话"
}

func continuationName(parentName string) string {
	return titleextract.ContinuationName(parentName)
}
