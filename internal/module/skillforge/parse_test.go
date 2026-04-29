package skillforge

import (
	"strings"
	"testing"
)

func TestParse_FrontmatterAndH2Sections(t *testing.T) {
	src := `---
name: 测试驱动开发
description: 实现任何功能前使用
---

# 测试驱动开发

## 红绿重构循环

先写失败测试，再实现，最后重构。

具体：
- 红：写测试

## 反模式与诊断

常见反模式列表。
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if got.Name != "测试驱动开发" {
		t.Errorf("Name = %q, want 测试驱动开发", got.Name)
	}
	if got.Description != "实现任何功能前使用" {
		t.Errorf("Description = %q", got.Description)
	}
	if len(got.Sections) != 2 {
		t.Fatalf("len(Sections) = %d, want 2", len(got.Sections))
	}
	if got.Sections[0].Title != "红绿重构循环" {
		t.Errorf("Sections[0].Title = %q", got.Sections[0].Title)
	}
	if !strings.Contains(got.Sections[0].Body, "先写失败测试") {
		t.Errorf("Sections[0].Body missing expected content")
	}
}

func TestParse_NoFrontmatterReturnsError(t *testing.T) {
	src := `# Title without frontmatter

## H2

Body
`
	_, err := Parse(src)
	if err == nil {
		t.Fatal("Parse should fail without frontmatter")
	}
}

func TestParse_NoH2SectionsIsAllowed(t *testing.T) {
	src := `---
name: simple
description: only intro
---

# Simple

Just intro text, no H2.
`
	got, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(got.Sections) != 0 {
		t.Errorf("len(Sections) = %d, want 0", len(got.Sections))
	}
}
