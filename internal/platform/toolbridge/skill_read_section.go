package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// ToolNameReadSection is the model-visible name for the skill_read_section host-direct tool.
const ToolNameReadSection = "skill_read_section"

// SkillReadSectionTool reads a reference section file from the skill cache directory.
// It calls skilllibrary.ReadSection to locate <cacheDir>/<name>/references/<NN-anchor>.md
// by anchor suffix, and optionally truncates the result to max_bytes.
type SkillReadSectionTool struct {
	cacheDir string
}

// NewSkillReadSectionTool constructs a SkillReadSectionTool that serves files from cacheDir.
func NewSkillReadSectionTool(cacheDir string) *SkillReadSectionTool {
	return &SkillReadSectionTool{cacheDir: cacheDir}
}

// skillReadSectionArgs holds the JSON-decoded arguments for a skill_read_section call.
type skillReadSectionArgs struct {
	Name     string `json:"name"`
	Anchor   string `json:"anchor"`
	MaxBytes int    `json:"max_bytes,omitempty"`
}

// Call executes the skill_read_section tool. raw must be a JSON object with fields
// name (string, required), anchor (string, required), and optionally max_bytes (int).
// Returns the raw file bytes; if max_bytes > 0 and the body exceeds that limit the
// result is truncated to exactly max_bytes bytes.
// All errors are wrapped with the "skill_read_section:" prefix.
func (t *SkillReadSectionTool) Call(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var a skillReadSectionArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("skill_read_section: parse args: %w", err)
	}
	body, err := skilllibrary.ReadSection(t.cacheDir, a.Name, a.Anchor)
	if err != nil {
		return nil, fmt.Errorf("skill_read_section: %w", err)
	}
	if a.MaxBytes > 0 && len(body) > a.MaxBytes {
		body = body[:a.MaxBytes]
	}
	return json.RawMessage(body), nil
}
