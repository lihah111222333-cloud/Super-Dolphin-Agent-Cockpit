package skillforge

import (
	"strings"
	"testing"
)

func TestRender_SlimSKILLMDContainsTOC(t *testing.T) {
	ps := &ParsedSkill{
		Name:        "测试驱动开发",
		Description: "实现任何功能前使用",
		Sections: []Section{
			{Title: "红绿重构循环", Body: "先写失败测试，再实现，最后重构。详细：..."},
			{Title: "反模式与诊断", Body: "常见反模式信号。"},
		},
	}
	out, err := Render(ps, nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	skillMD := out.SkillMD
	if !strings.Contains(skillMD, "name: 测试驱动开发") {
		t.Errorf("SkillMD missing frontmatter name")
	}
	if !strings.Contains(skillMD, "01-红绿重构循环.md") {
		t.Errorf("SkillMD missing references[0] link")
	}
	if !strings.Contains(skillMD, "先写失败测试") {
		t.Errorf("SkillMD missing summary[0]")
	}
	if len(out.References) != 2 {
		t.Fatalf("len(References) = %d, want 2", len(out.References))
	}
	if !strings.Contains(out.References["01-红绿重构循环.md"], "先写失败测试") {
		t.Errorf("references[01].body missing original content")
	}
}

func TestRender_RespectsSummaryOverride(t *testing.T) {
	ps := &ParsedSkill{
		Name:        "demo",
		Description: "d",
		Sections:    []Section{{Title: "节A", Body: "原文摘要"}},
	}
	override := map[string]string{"节A": "手写摘要覆盖"}
	out, err := Render(ps, override)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out.SkillMD, "手写摘要覆盖") {
		t.Errorf("SkillMD did not pick up overridden summary")
	}
	if strings.Contains(out.SkillMD, "原文摘要") {
		t.Errorf("SkillMD should not contain auto summary when override present")
	}
}

func TestRender_NilParsedSkillReturnsError(t *testing.T) {
	_, err := Render(nil, nil)
	if err == nil {
		t.Fatal("Render(nil) should return error")
	}
}

func TestRender_NoSectionsProducesValidOutput(t *testing.T) {
	ps := &ParsedSkill{
		Name:        "intro-only",
		Description: "no h2",
		Sections:    nil,
	}
	out, err := Render(ps, nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !strings.Contains(out.SkillMD, "name: intro-only") {
		t.Errorf("SkillMD missing frontmatter")
	}
	if len(out.References) != 0 {
		t.Errorf("len(References) = %d, want 0", len(out.References))
	}
}

func TestRender_DuplicateH2TitlesDisambiguatedByIndex(t *testing.T) {
	// 两个同名 H2 应该用索引区分文件名。
	ps := &ParsedSkill{
		Name:        "x",
		Description: "d",
		Sections: []Section{
			{Title: "节A", Body: "first"},
			{Title: "节A", Body: "second"},
		},
	}
	out, err := Render(ps, nil)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if len(out.References) != 2 {
		t.Fatalf("len(References) = %d, want 2", len(out.References))
	}
	if _, ok := out.References["01-节A.md"]; !ok {
		t.Errorf("missing references[01-节A.md]")
	}
	if _, ok := out.References["02-节A.md"]; !ok {
		t.Errorf("missing references[02-节A.md]")
	}
}
