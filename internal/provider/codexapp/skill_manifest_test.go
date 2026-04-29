package codexapp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

func TestBuildSkillManifest_BasicL1C(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{
			Meta: &skilllibrary.SkillMeta{
				Name: "测试驱动开发",
				SectionSummaries: map[string]string{
					"红绿重构": "三步循环",
					"反模式":  "常见信号",
				},
			},
			SkillMD: "---\nname: 测试驱动开发\ndescription: 实现功能前使用\n---\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "测试驱动开发") {
		t.Error("missing skill name")
	}
	if !strings.Contains(out, "实现功能前使用") {
		t.Error("missing description")
	}
	if !strings.Contains(out, "skill_read_section") {
		t.Error("missing tool hint")
	}
	if !strings.Contains(out, "红绿重构") {
		t.Error("missing first anchor")
	}
	if !strings.Contains(out, "三步循环") {
		t.Error("missing first summary")
	}
}

func TestBuildSkillManifest_EmptyEntries(t *testing.T) {
	out := buildSkillManifest(nil, 8192)
	if out != "" {
		t.Errorf("empty entries should produce empty output, got %q", out)
	}
}

func TestBuildSkillManifest_BudgetTruncation(t *testing.T) {
	var entries []skilllibrary.SkillEntry
	for i := 0; i < 100; i++ {
		entries = append(entries, skilllibrary.SkillEntry{
			Meta:    &skilllibrary.SkillMeta{Name: fmt.Sprintf("skill%03d", i)},
			SkillMD: fmt.Sprintf("---\nname: skill%03d\ndescription: %s\n---\n", i, strings.Repeat("x", 80)),
		})
	}
	out := buildSkillManifest(entries, 1024)
	if len(out) > 1500 {
		t.Errorf("output exceeds budget+headroom: %d", len(out))
	}
	if !strings.Contains(out, "截断省略") {
		t.Error("expected truncation marker when budget exhausted")
	}
}

func TestBuildSkillManifest_StableSort(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{Meta: &skilllibrary.SkillMeta{Name: "zebra"}, SkillMD: "---\nname: zebra\ndescription: z\n---\n"},
		{Meta: &skilllibrary.SkillMeta{Name: "alpha"}, SkillMD: "---\nname: alpha\ndescription: a\n---\n"},
	}
	out := buildSkillManifest(entries, 8192)
	alphaIdx := strings.Index(out, "alpha")
	zebraIdx := strings.Index(out, "zebra")
	if alphaIdx < 0 || zebraIdx < 0 {
		t.Fatalf("missing both names: %s", out)
	}
	if alphaIdx > zebraIdx {
		t.Error("alpha should come before zebra (sorted)")
	}
}

func TestBuildSkillManifest_NoSectionsSkillRendersOnlyName(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{
			Meta:    &skilllibrary.SkillMeta{Name: "x"},
			SkillMD: "---\nname: x\ndescription: just a stub\n---\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if strings.Contains(out, "节索引") {
		t.Error("should not emit 节索引 when SectionSummaries is empty")
	}
	if !strings.Contains(out, "just a stub") {
		t.Error("missing description")
	}
}

func TestBuildSkillManifest_HandlesQuotedDescription(t *testing.T) {
	// 旧 extractDescriptionFromSkillMD 用 HasPrefix("description:") 简单截，
	// 对带引号 + 含逗号的 description 静默丢字段。改用 skillforge.Parse 后必须
	// 正确解析。
	entries := []skilllibrary.SkillEntry{
		{
			Meta:    &skilllibrary.SkillMeta{Name: "x"},
			SkillMD: "---\nname: x\ndescription: \"含逗号的描述, 测试解析\"\n---\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "含逗号的描述, 测试解析") {
		t.Errorf("manifest 未正确解析带引号 description:\n%s", out)
	}
}

func TestBuildSkillManifest_HandlesCRLFFrontmatter(t *testing.T) {
	entries := []skilllibrary.SkillEntry{
		{
			Meta:    &skilllibrary.SkillMeta{Name: "y"},
			SkillMD: "---\r\nname: y\r\ndescription: 有 CRLF 的描述\r\n---\r\n",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "有 CRLF 的描述") {
		t.Errorf("manifest 未正确处理 CRLF frontmatter:\n%s", out)
	}
}

func TestBuildSkillManifest_MalformedFrontmatterReturnsEmptyDesc(t *testing.T) {
	// frontmatter 缺失也不应 panic，仅 description 为空。
	entries := []skilllibrary.SkillEntry{
		{
			Meta:    &skilllibrary.SkillMeta{Name: "z"},
			SkillMD: "no frontmatter at all",
		},
	}
	out := buildSkillManifest(entries, 8192)
	if !strings.Contains(out, "- z — \n") {
		t.Errorf("malformed SkillMD 应渲染空 description:\n%s", out)
	}
}
