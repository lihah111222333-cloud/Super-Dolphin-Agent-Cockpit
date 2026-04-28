package claudecli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// TestBuildSkillList_FiltersNoneMode P20.1 §3.3 加固：
// Mode=None 的 skill 必须不出现在 "skills: - xxx" 列表里，否则模型会看见名字
// 但找不到 body。本测试用最小桩数据锁定该不变量。
func TestBuildSkillList_FiltersNoneMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "visible-full", Mode: dto.SkillModeFull, Prompt: "ignored by list fn"},
		{Name: "hidden-none", Mode: dto.SkillModeNone, Prompt: "will not be listed"},
		{Name: "visible-summary", Mode: dto.SkillModeSummary, Summary: "s"},
		{Name: "unspecified-defaults-full", Prompt: "no mode field"},
	}
	list := buildSkillList(skills)
	if !strings.Contains(list, "visible-full") {
		t.Fatalf("Full skill must appear in list: %q", list)
	}
	if !strings.Contains(list, "visible-summary") {
		t.Fatalf("Summary skill must appear in list: %q", list)
	}
	if !strings.Contains(list, "unspecified-defaults-full") {
		t.Fatalf("Unspecified (legacy) should default to full and appear: %q", list)
	}
	if strings.Contains(list, "hidden-none") {
		t.Fatalf("None-mode skill MUST NOT appear in list (P20.1 §3.3): %q", list)
	}
}

// TestBuildSkillList_FiltersInvalidMode 非法 mode（经 Effective() 降级为 None）
// 同样不得暴露名字。
func TestBuildSkillList_FiltersInvalidMode(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "good", Mode: dto.SkillModeFull, Prompt: "x"},
		{Name: "bad-mode", Mode: "banana", Prompt: "x"}, // Effective → None
	}
	list := buildSkillList(skills)
	if !strings.Contains(list, "good") {
		t.Fatalf("good skill should appear: %q", list)
	}
	if strings.Contains(list, "bad-mode") {
		t.Fatalf("invalid-mode skill MUST NOT appear: %q", list)
	}
}

// TestBuildSkillList_EmptyWhenAllFiltered 若所有 skill 都被过滤掉（None / 无名），
// buildSkillList 应返回空串（不输出孤零零的 "skills:" header）。
func TestBuildSkillList_EmptyWhenAllFiltered(t *testing.T) {
	skills := []dto.SkillRef{
		{Name: "all-none", Mode: dto.SkillModeNone},
		{Name: "", Mode: dto.SkillModeFull},
		{Name: "another-none", Mode: dto.SkillModeNone},
	}
	if list := buildSkillList(skills); list != "" {
		t.Fatalf("all-filtered case should yield empty list, got %q", list)
	}
}

// ============================================================================
// P20.1 Phase 7 SkillInjectionPort (claudecli)
// ============================================================================

// TestClaudecliNativeScanRequiresCWD is the P22 P4 fail-closed guard for
// the native-scan authoritative root path: an empty or whitespace-only
// cwd must produce contract.ErrMissingCWD, not a silent nil. Pre-P22 the
// method returned nil in both branches, letting callers mix "no project
// root" and "no native skills" into the same observation. P4 §138 and
// §270-272 explicitly forbid that fallback.
func TestClaudecliNativeScanRequiresCWD(t *testing.T) {
	port := NewSkillInjectionPort()
	for _, cwd := range []string{"", "   ", "\t"} {
		names, err := port.DetectNativeSkills(cwd)
		if !errors.Is(err, contract.ErrMissingCWD) {
			t.Errorf("cwd=%q: err = %v, want contract.ErrMissingCWD", cwd, err)
		}
		if names != nil {
			t.Errorf("cwd=%q: names = %v, want nil", cwd, names)
		}
	}
}

func TestClaudecliSkillInjectionPort_DetectNativeSkills_Empty(t *testing.T) {
	port := NewSkillInjectionPort()
	// Empty cwd is now ErrMissingCWD (see TestClaudecliNativeScanRequiresCWD
	// above); this test locks the benign ".claude/skills missing" branch.
	names, err := port.DetectNativeSkills(t.TempDir())
	if err != nil {
		t.Fatalf("tmp dir without .claude/skills: err = %v, want nil", err)
	}
	if names != nil {
		t.Fatalf("tmp dir without .claude/skills: %v, want nil", names)
	}
}

func TestClaudecliSkillInjectionPort_DetectNativeSkills_ScansClaudeSkillsDir(t *testing.T) {
	tmp := t.TempDir()
	skillsRoot := filepath.Join(tmp, ".claude", "skills")
	// 构造 2 个合法 skill 目录 + 1 个无 SKILL.md + 1 个普通文件
	for _, name := range []string{"foo", "bar-baz", "extra"} {
		if err := os.MkdirAll(filepath.Join(skillsRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	// foo, bar-baz 有 SKILL.md；extra 无
	if err := os.WriteFile(filepath.Join(skillsRoot, "foo", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write foo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "bar-baz", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write bar-baz: %v", err)
	}
	// 普通文件混进去应被跳过
	if err := os.WriteFile(filepath.Join(skillsRoot, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	// `.` 开头隐藏目录应被跳过
	if err := os.MkdirAll(filepath.Join(skillsRoot, ".hidden"), 0o755); err != nil {
		t.Fatalf("mkdir hidden: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, ".hidden", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}

	port := NewSkillInjectionPort()
	got, err := port.DetectNativeSkills(tmp)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	// 期望：只 foo 和 bar-baz，按字典序 (bar-baz, foo)
	if len(got) != 2 {
		t.Fatalf("expected 2 skills, got %d: %v", len(got), got)
	}
	if got[0] != "bar-baz" || got[1] != "foo" {
		t.Fatalf("result should be sorted lowercase, got %v", got)
	}
}

func TestClaudecliSkillInjectionPort_DetectNativeSkills_NameNormalized(t *testing.T) {
	tmp := t.TempDir()
	skillsRoot := filepath.Join(tmp, ".claude", "skills")
	// 目录名含大写——实测：macOS 默认文件系统不区分大小写，但仍返回原样
	if err := os.MkdirAll(filepath.Join(skillsRoot, "MixedCase"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "MixedCase", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	port := NewSkillInjectionPort()
	got, err := port.DetectNativeSkills(tmp)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0] != "mixedcase" {
		t.Fatalf("names should be lowered, got %v", got)
	}
}

func TestClaudecliSkillInjectionPort_ReservedTokens(t *testing.T) {
	port := NewSkillInjectionPort()
	if got := port.ReservedTokens(); got != 3000 {
		t.Fatalf("token budget = %d, want 3000", got)
	}
}

// TestClaudecliSkillInjectionPort_DetectNativeSkills_SkillMdAsDirectoryIgnored
// 防御错误布局：若某个 skill 目录下的 "SKILL.md" 自身是一个子目录（而非普通
// 文件），该 skill 应被跳过，不得误判为有效原生 skill。
func TestClaudecliSkillInjectionPort_DetectNativeSkills_SkillMdAsDirectoryIgnored(t *testing.T) {
	tmp := t.TempDir()
	skillsRoot := filepath.Join(tmp, ".claude", "skills")

	// foo: SKILL.md 是子目录（布局错误）
	if err := os.MkdirAll(filepath.Join(skillsRoot, "foo", "SKILL.md"), 0o755); err != nil {
		t.Fatalf("mkdir foo/SKILL.md as dir: %v", err)
	}
	// bar: SKILL.md 是普通文件（正常）
	if err := os.MkdirAll(filepath.Join(skillsRoot, "bar"), 0o755); err != nil {
		t.Fatalf("mkdir bar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillsRoot, "bar", "SKILL.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write bar: %v", err)
	}

	port := NewSkillInjectionPort()
	got, err := port.DetectNativeSkills(tmp)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 || got[0] != "bar" {
		t.Fatalf("foo should be skipped (SKILL.md is a directory), got %v", got)
	}
}

// TestClaudecliSkillInjectionPort_DetectNativeSkills_StableOrdering 连续多次
// 调用，结果必须按字典序一致——防御未来有人删 sort.Strings() 破坏
// ApplyNativeSkillOverride 下游的 tie-break 稳定性。
func TestClaudecliSkillInjectionPort_DetectNativeSkills_StableOrdering(t *testing.T) {
	tmp := t.TempDir()
	skillsRoot := filepath.Join(tmp, ".claude", "skills")
	// 故意以非字典序创建
	for _, name := range []string{"zulu", "alpha", "mike", "charlie"} {
		if err := os.MkdirAll(filepath.Join(skillsRoot, name), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(skillsRoot, name, "SKILL.md"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	port := NewSkillInjectionPort()
	expected := []string{"alpha", "charlie", "mike", "zulu"}
	for i := 0; i < 3; i++ {
		got, err := port.DetectNativeSkills(tmp)
		if err != nil {
			t.Fatalf("run %d: unexpected err: %v", i, err)
		}
		if len(got) != 4 {
			t.Fatalf("run %d: len=%d", i, len(got))
		}
		for j, want := range expected {
			if got[j] != want {
				t.Fatalf("run %d: got[%d]=%q want %q (full=%v)", i, j, got[j], want, got)
			}
		}
	}
}
