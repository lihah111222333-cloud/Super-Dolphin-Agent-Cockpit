package archtest_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestVideoGenerateSkillUsesVideoWithAudio(t *testing.T) {
	body := readTextFile(t, filepath.Join(repoRoot(t), ".agent/skills/video-generate/SKILL.md"))

	for _, want := range []string{
		`name: "video-generate"`,
		"**必须调用 `video_with_audio` MCP tool**",
		"`voice_text`",
		"- 禁止调用 `video_generate`",
		"- 禁止分开调用 `tts_generate`、`av_merge`",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("video-generate skill missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"**必须调用 `video_generate` MCP tool**",
		"调用 `video_generate` tool",
		"等待 tool 返回视频 URL",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("video-generate skill keeps obsolete no-audio flow %q", forbidden)
		}
	}
}
