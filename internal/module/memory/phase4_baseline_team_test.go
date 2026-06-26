package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// 这些基线测试覆盖 team manager 非空时的入口注入路径。
// 既有 entrypoint_provider_test 主要走 team=nil；这里锁住 team block 注入，避免后续排序调整静默漏掉团队记忆。
//
// runtime-ready gate 是进程级开关；本文件通过 withTeamMemoryRuntimeReady 安装逐测函数指针，
// 并用 Cleanup 恢复旧值，避免同包并发测试争用同一个 atomic.Bool。因此这些测试不调用 t.Parallel()。

func TestPhase4BaselineEntrypointProviderInjectsTeamBlock(t *testing.T) {
	t.Setenv(envHarnessKind, "")
	withTeamMemoryRuntimeReady(t, true)

	cfg := newPhase4BaselineConfig(t, true)
	writePhase4BaselineMemory(t, cfg.AutoMemPathOverride, "- [Architecture](architecture.md) — start here")
	writePhase4BaselineMemory(t, mustConfiguredTeamMemRoot(t, cfg), "- [Dashboard owner](owner.md) — team-side guidance")

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: cfg.ProjectRoot, GitRoot: cfg.ProjectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want wrapped block with team injection")
	}
	got := *out
	assertPhase4BaselineContainsAll(t, got, "team=non-nil baseline", []string{
		"source=auto",
		"source=team",
		"Dashboard owner",
		"Architecture",
	})
}

func TestPhase4BaselineEntrypointProviderTeamDisabledOmitsTeamBlock(t *testing.T) {
	// runtime-ready 保持开启，专门验证入口注入只受 TeamMemEnabled 控制。
	// 如果未来把 runtime-ready 折进入口短路条件，TeamMemEnabled=false 的反例会立刻失败。
	t.Setenv(envHarnessKind, "")
	withTeamMemoryRuntimeReady(t, true)
	cfg := newPhase4BaselineConfig(t, false)
	writePhase4BaselineMemory(t, cfg.AutoMemPathOverride, "- [Private only](private.md)")

	team := NewTeamMemoryManager(cfg)
	provider := NewEntrypointProvider(cfg, team, nil)
	out, err := provider.Resolve(context.Background(), contract.SectionContext{
		Start:    &contract.StartInput{},
		BuildCtx: contract.BuildCtx{CWD: cfg.ProjectRoot, GitRoot: cfg.ProjectRoot},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if out == nil {
		t.Fatal("Resolve() = nil, want private-only block")
	}
	got := *out
	assertPhase4BaselineOmits(t, got, "source=team", "TeamMemory=false")
	assertPhase4BaselineContainsAll(t, got, "private-only block", []string{"source=auto"})
}

func newPhase4BaselineConfig(t *testing.T, enableTeam bool) *Config {
	t.Helper()
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         newTestGitProjectRoot(t),
		AutoMemPathOverride: filepath.Join(t.TempDir(), "private"),
	}
	if enableTeam {
		cfg.Features = MemoryFeatureFlags{TeamMemory: true}
	}
	return cfg
}

func mustConfiguredTeamMemRoot(t *testing.T, cfg *Config) string {
	t.Helper()
	teamRoot, err := configuredTeamMemRoot(cfg)
	if err != nil {
		t.Fatalf("configuredTeamMemRoot() error = %v", err)
	}
	return teamRoot
}

func writePhase4BaselineMemory(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", root, err)
	}
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
}

func assertPhase4BaselineContainsAll(t *testing.T, got, context string, values []string) {
	t.Helper()
	for _, value := range values {
		if !strings.Contains(got, value) {
			t.Fatalf("Resolve() missing %s marker %q:\n%s", context, value, got)
		}
	}
}

func assertPhase4BaselineOmits(t *testing.T, got, value, context string) {
	t.Helper()
	if strings.Contains(got, value) {
		t.Fatalf("Resolve() leaked %s under %s:\n%s", value, context, got)
	}
}

// 该测试锁定私有与团队作用域同名条目的文件隔离。
// 这个用例只验证 FilePath 不共享；排序分值差异需要另起测试覆盖，不能从本断言推断。
func TestPhase4BaselineCrossScopeFilePathDisjoint(t *testing.T) {
	// 使用两个完全独立的临时根，避免默认嵌套布局让 BuildManifest 跨作用域 walkDir。
	privateRoot, teamRoot := newPhase4CrossScopeRoots(t)

	const sharedName = "Cross-scope baseline name"

	createPhase4CrossScopeEntry(t, privateRoot, MemoryWriteRequest{
		Name:        sharedName,
		Description: "private-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: lock cross-scope baseline.\nHow to apply: see team variant for rationale.",
	})
	createPhase4CrossScopeEntry(t, teamRoot, MemoryWriteRequest{
		Name:        sharedName,
		Description: "team-side same-name fixture",
		Type:        MemoryTypeFeedback,
		Body:        "fact\nWhy: cross-team coordination.\nHow to apply: this is the project-wide variant.",
	})

	builder := NewManifestBuilder()
	privateFilePath := buildPhase4CrossScopeFilePath(t, builder, privateRoot, sharedName, "private side")
	teamFilePath := buildPhase4CrossScopeFilePath(t, builder, teamRoot, sharedName, "team side")
	if privateFilePath == teamFilePath {
		t.Fatalf("baseline expected distinct FilePath across scopes; got both=%q", privateFilePath)
	}
	// 这里仅确认路径隔离；同名条目的排序优势要由排序管线测试单独断言。
}

func newPhase4CrossScopeRoots(t *testing.T) (string, string) {
	t.Helper()
	privateRoot := filepath.Join(t.TempDir(), "private-scope")
	teamRoot := filepath.Join(t.TempDir(), "team-scope")
	mkdirPhase4Root(t, privateRoot, "privateRoot")
	mkdirPhase4Root(t, teamRoot, "teamRoot")
	return privateRoot, teamRoot
}

func mkdirPhase4Root(t *testing.T, root, label string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", label, err)
	}
}

func createPhase4CrossScopeEntry(t *testing.T, root string, req MemoryWriteRequest) {
	t.Helper()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore(%s) error = %v", root, err)
	}
	if _, err := store.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(%s) error = %v", root, err)
	}
}

func buildPhase4CrossScopeFilePath(t *testing.T, builder *ManifestBuilder, root, sharedName, label string) string {
	t.Helper()
	entries, err := builder.BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest(%s) error = %v", label, err)
	}
	hits := findEntriesByName(entries, sharedName)
	if len(hits) != 1 {
		t.Fatalf("%s: hits for %q = %d, want 1; entries=%#v", label, sharedName, len(hits), entries)
	}
	return hits[0].FilePath
}
