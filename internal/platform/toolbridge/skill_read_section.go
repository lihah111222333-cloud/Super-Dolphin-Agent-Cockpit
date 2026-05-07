package toolbridge

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ToolNameReadSection is the model-visible name for the skill_read_section host-direct tool.
const ToolNameReadSection = "skill_read_section"

// SkillReadSectionTool reads a reference section file from the skill cache directory.
// It calls the injected SkillSectionReader to locate <cacheDir>/<name>/references/<NN-anchor>.md
// by anchor suffix, and optionally truncates the result to max_bytes.
//
// P6: when recorder is non-nil, every successful Call records a
// CallEvent (skill name + anchor) for FBSD frequency-based tier ranking.
type SkillReadSectionTool struct {
	cacheDir string
	reader   contract.SkillSectionReader // injected; reads section from cache
	recorder contract.SkillCallRecorder  // nil-safe, optional
}

// NewSkillReadSectionTool constructs a SkillReadSectionTool that serves files from cacheDir.
// reader is the section-reading function (production: skilllibrary.ReadSection).
// recorder is optional (nil-safe); when non-nil, FBSD打点 happens
// after every successful Call.
func NewSkillReadSectionTool(cacheDir string, reader contract.SkillSectionReader, recorder contract.SkillCallRecorder) *SkillReadSectionTool {
	return &SkillReadSectionTool{cacheDir: cacheDir, reader: reader, recorder: recorder}
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
	a, err := decodeSkillReadSectionArgs(raw)
	if err != nil {
		return nil, err
	}
	result, err := t.readSection(a)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(result.body), nil
}

type skillReadSectionPayload struct {
	body       []byte
	totalBytes int
	truncated  bool
}

func (t *SkillReadSectionTool) readSection(a skillReadSectionArgs) (skillReadSectionPayload, error) {
	body, err := t.reader(t.cacheDir, a.Name, a.Anchor)
	if err != nil {
		return skillReadSectionPayload{}, fmt.Errorf("skill_read_section: %w", err)
	}
	totalBytes := len(body)
	truncated := false
	if a.MaxBytes > 0 && len(body) > a.MaxBytes {
		body = body[:a.MaxBytes]
		truncated = true
	}
	// P6 FBSD 打点：成功 ReadSection 后异步记录调用频次。recorder 为 nil 时
	// 跳过打点（向后兼容）。
	if t.recorder != nil {
		t.recorder.Record(a.Name, a.Anchor)
	}
	return skillReadSectionPayload{body: body, totalBytes: totalBytes, truncated: truncated}, nil
}
