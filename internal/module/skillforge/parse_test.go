package skillforge

import (
	"errors"
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
	if !errors.Is(err, ErrMissingFrontmatter) {
		t.Errorf("err = %v, want errors.Is ErrMissingFrontmatter", err)
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

func TestParse_EdgeCases(t *testing.T) {
	cases := []struct {
		name      string
		src       string
		wantName  string
		wantSects int
	}{
		{
			name:      "CRLF line endings",
			src:       "---\r\nname: x\r\ndescription: d\r\n---\r\n# x\r\n\r\n## A\r\n\r\nbody A\r\n\r\n## B\r\n\r\nbody B\r\n",
			wantName:  "x",
			wantSects: 2,
		},
		{
			name:      "colon in quoted value",
			src:       "---\nname: x\ndescription: \"use, e.g., this:that\"\n---\n# x\n",
			wantName:  "x",
			wantSects: 0,
		},
		{
			name:      "zero-length frontmatter",
			src:       "---\n---\n# Body only\n",
			wantName:  "",
			wantSects: 0,
		},
		{
			name:      "H2 with empty body",
			src:       "---\nname: x\ndescription: d\n---\n# x\n\n## A\n## B\n",
			wantName:  "x",
			wantSects: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if len(got.Sections) != tc.wantSects {
				t.Errorf("len(Sections) = %d, want %d", len(got.Sections), tc.wantSects)
			}
		})
	}
	// Verify "colon in quoted value" actually preserves the colon
	ps, err := Parse("---\nname: x\ndescription: \"use, e.g., this:that\"\n---\n# x\n")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if ps.Description != "use, e.g., this:that" {
		t.Errorf("Description = %q, want %q", ps.Description, "use, e.g., this:that")
	}
}
