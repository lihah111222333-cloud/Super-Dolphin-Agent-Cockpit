package prompt

import (
	"context"
	"strings"
	"testing"
)

// TestAssembleStart_CachedPrefixBytewiseStable 验证相同 BuildCtx 和冻结日期会生成字节稳定的启动前缀。
// CachedPrefix 会进入 Claude CLI 的 --system-prompt；跨 turn 不抖动时，provider 侧短 TTL prompt cache 才能命中。
func TestAssembleStart_CachedPrefixBytewiseStable(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")
	cwd := t.TempDir()

	newAssembly := func() StartAssembly {
		svc := NewService(&Config{}, nil)
		got, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      cwd,
			Language: "English",
			Model:    "claude-sonnet-4",
		})
		if err != nil {
			t.Fatalf("AssembleStart() error = %v", err)
		}
		return got
	}

	first := newAssembly()
	second := newAssembly()

	if first.Boundary == nil || second.Boundary == nil {
		t.Fatalf("Boundary missing: first=%#v second=%#v", first.Boundary, second.Boundary)
	}
	if first.Boundary.CachedPrefix != second.Boundary.CachedPrefix {
		t.Fatalf("CachedPrefix diverged:\nfirst  = %q\nsecond = %q",
			first.Boundary.CachedPrefix, second.Boundary.CachedPrefix)
	}
	if strings.TrimSpace(first.Boundary.CachedPrefix) == "" {
		t.Fatal("CachedPrefix is empty; static sections should always produce content")
	}
	// BaseInstructions 也必须字节稳定；每轮变化的 user meta 和 System Context 走结构化字段。
	if first.BaseInstructions != second.BaseInstructions {
		t.Fatalf("BaseInstructions diverged across calls with same BuildCtx:\nfirst  = %q\nsecond = %q",
			first.BaseInstructions, second.BaseInstructions)
	}
	// 防止旧泄漏形状回归：裸标签可能在正文中被提及，真正危险的是 userMeta 包装块。
	if strings.Contains(first.BaseInstructions, "Today's date is") {
		t.Fatalf("BaseInstructions leaked currentDate payload; userMeta must live in StartAssembly.UserContext only:\n%s",
			first.BaseInstructions)
	}
	if strings.Contains(first.BaseInstructions, "# System Context") {
		t.Fatalf("BaseInstructions leaked System Context block; gitStatus must live in StartAssembly.SystemContext only:\n%s",
			first.BaseInstructions)
	}
	// 同一份数据必须保留在结构化字段中，供 provider bridge 注入合成 user meta。
	if _, ok := first.UserContext["currentDate"]; !ok {
		t.Fatalf("UserContext missing currentDate after Phase 3 split: %#v", first.UserContext)
	}
}

// TestAssembleStart_PopulatesUserMetaFields 验证 StartAssembly 暴露结构化 user/system context。
// provider bridge 必须从这些字段读取动态上下文，不能再从 BaseInstructions 字符串里反解析。
func TestAssembleStart_PopulatesUserMetaFields(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      t.TempDir(),
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}

	date, ok := assembly.UserContext["currentDate"]
	if !ok {
		t.Fatalf("UserContext missing currentDate: %#v", assembly.UserContext)
	}
	if !strings.Contains(date, "2026-04-22") {
		t.Fatalf("UserContext[currentDate] = %q, want the frozen date", date)
	}
	if !strings.Contains(assembly.UserContextText, "currentDate") {
		t.Fatalf("UserContextText missing currentDate heading: %q", assembly.UserContextText)
	}
	// CWD 不是 git repo 且未配置 cache breaker 时 SystemContext 可为空；一旦存在则每项都必须非空。
	for key, value := range assembly.SystemContext {
		if strings.TrimSpace(value) == "" {
			t.Fatalf("SystemContext[%q] populated but empty", key)
		}
	}
}

// TestSimpleStartAssembly_ThreeLineForm 验证 CLAUDE_CODE_SIMPLE 快速路径只输出固定三行格式。
// 该路径刻意不填充 UserContext/UserContextText/SystemContext，保持 provider 最小启动面。
func TestSimpleStartAssembly_ThreeLineForm(t *testing.T) {
	t.Setenv(envClaudeSimple, "1")
	t.Setenv(envPromptStartCurrentDate, "2026-04-22")

	svc := NewService(&Config{}, nil)
	cwd := t.TempDir()
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	want := simpleStartIdentityLine + "\nCWD: " + cwd + "\nDate: 2026-04-22"
	if assembly.BaseInstructions != want {
		t.Fatalf("BaseInstructions = %q, want strict three-line form %q", assembly.BaseInstructions, want)
	}
	if assembly.UserContext != nil || assembly.UserContextText != "" || assembly.SystemContext != nil {
		t.Fatalf("ultraSimple must leave UserContext/SystemContext empty: %#v / %q / %#v", assembly.UserContext, assembly.UserContextText, assembly.SystemContext)
	}
}
