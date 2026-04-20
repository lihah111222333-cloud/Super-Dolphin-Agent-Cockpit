package skill

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestParseSkillRecord_RejectsOversizedFile 防 DoS：恶意项目在 .agent/skills/evil/SKILL.md
// 塞进大于 maxSkillFileBytes (1MB) 的内容，扫盘期必须拒绝而不是读入内存。
// 这条回归防止未来重构误删 size check 。
func TestParseSkillRecord_RejectsOversizedFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "evil")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	// 书写比 maxSkillFileBytes 略大的内容（1MB + 1 字节）
	big := bytes.Repeat([]byte("x"), maxSkillFileBytes+1)
	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatalf("write oversized: %v", err)
	}
	_, err := parseSkillRecord(tmp, path, TrustProject)
	if err == nil {
		t.Fatalf("expected error for oversized SKILL.md, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error should mention size limit: %v", err)
	}
}

// TestParseSkillRecord_AcceptsNormalFile 确保大小检查不会误伤正常文件。
func TestParseSkillRecord_AcceptsNormalFile(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, skillMainFile)
	content := "---\nname: foo\ndescription: hi\n---\nbody"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	rec, err := parseSkillRecord(tmp, path, TrustProject)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.info.Name != "foo" {
		t.Fatalf("name = %q", rec.info.Name)
	}
}

// helperParse wraps parseSkillInfo with a fixed rel/dir so tests focus on content parsing.
func helperParse(t *testing.T, content string, defaultTrust TrustScope) SkillInfo {
	t.Helper()
	return parseSkillInfo("foo", "/tmp/foo", content, defaultTrust)
}

func TestParseSkillInfo_ContentHashStableAndCorrect(t *testing.T) {
	content := "---\nname: foo\ndescription: test\n---\n\nhello"
	info := helperParse(t, content, TrustProject)
	sum := sha256.Sum256([]byte(content))
	want := hex.EncodeToString(sum[:])
	if info.ContentHash != want {
		t.Fatalf("content hash = %q want %q", info.ContentHash, want)
	}
	// 确认同输入产生同哈希（确定性）
	again := helperParse(t, content, TrustProject)
	if again.ContentHash != info.ContentHash {
		t.Fatalf("hash not stable: %q vs %q", again.ContentHash, info.ContentHash)
	}
}

func TestParseSkillInfo_FrontmatterTrustOverridesDefault(t *testing.T) {
	content := "---\nname: foo\ntrust: signed\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if info.Trust != TrustSigned {
		t.Fatalf("frontmatter trust should win: got %q", info.Trust)
	}
}

func TestParseSkillInfo_DefaultTrustWhenFrontmatterMissing(t *testing.T) {
	content := "---\nname: foo\ndescription: hi\n---\n\nbody"
	info := helperParse(t, content, TrustUser)
	if info.Trust != TrustUser {
		t.Fatalf("default trust should apply: got %q", info.Trust)
	}
}

func TestParseSkillInfo_SafetyFallbackToProject(t *testing.T) {
	content := "body only, no frontmatter"
	info := helperParse(t, content, TrustUnknown)
	if info.Trust != TrustProject {
		t.Fatalf("safety fallback should be TrustProject: got %q", info.Trust)
	}
}

// 非法 trust 值（如 "banana"）应被 parseTrustScope 返回 TrustUnknown，
// 使 parseSkillInfo 的回填逻辑回落到 defaultTrust，而不是被误写为 TrustUnknown。
func TestParseSkillInfo_InvalidFrontmatterTrustUsesDefault(t *testing.T) {
	content := "---\nname: foo\ntrust: banana\n---\nbody"
	info := helperParse(t, content, TrustUser)
	if info.Trust != TrustUser {
		t.Fatalf("invalid trust value should fall back to default (TrustUser): got %q", info.Trust)
	}
}

// defaultTrust 也非法时（TrustUnknown）最终应回落到安全兑底 TrustProject。
func TestParseSkillInfo_InvalidFrontmatterAndInvalidDefault_FallbackToProject(t *testing.T) {
	content := "---\nname: foo\ntrust: nonsense\n---\nbody"
	info := helperParse(t, content, TrustUnknown)
	if info.Trust != TrustProject {
		t.Fatalf("both invalid should safety-fallback to TrustProject: got %q", info.Trust)
	}
}

func TestParseSkillInfo_AllowedTools(t *testing.T) {
	content := "---\nname: foo\nallowed-tools: [Read, skill_expand]\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if len(info.AllowedTools) != 2 {
		t.Fatalf("AllowedTools len = %d, want 2: %v", len(info.AllowedTools), info.AllowedTools)
	}
	joined := strings.Join(info.AllowedTools, ",")
	if !strings.Contains(joined, "Read") || !strings.Contains(joined, "skill_expand") {
		t.Fatalf("AllowedTools missing values: %v", info.AllowedTools)
	}
}

func TestParseSkillInfo_AllowedToolsAliases(t *testing.T) {
	// allowed_tools 下划线别名
	content := "---\nname: foo\nallowed_tools: Read,Bash\n---\n\nbody"
	info := helperParse(t, content, TrustProject)
	if len(info.AllowedTools) != 2 {
		t.Fatalf("expected 2 tools via alias, got %v", info.AllowedTools)
	}
}

func TestParseSkillInfo_DisableModelInvocationTrue(t *testing.T) {
	for _, truthy := range []string{"true", "yes", "y", "on", "1", "True", "YES"} {
		content := "---\nname: foo\ndisable-model-invocation: " + truthy + "\n---\n\nbody"
		info := helperParse(t, content, TrustProject)
		if !info.DisableModelInvocation {
			t.Fatalf("%q should parse as true", truthy)
		}
	}
}

func TestParseSkillInfo_DisableModelInvocationFalse(t *testing.T) {
	for _, falsy := range []string{"false", "no", "0", "off", "", "random"} {
		content := "---\nname: foo\ndisable-model-invocation: " + falsy + "\n---\n\nbody"
		info := helperParse(t, content, TrustProject)
		if info.DisableModelInvocation {
			t.Fatalf("%q should parse as false", falsy)
		}
	}
}

func TestParseBoolScalar(t *testing.T) {
	if !parseBoolScalar("true") || !parseBoolScalar(" YES ") || !parseBoolScalar("1") || !parseBoolScalar("on") {
		t.Fatalf("truthy cases failed")
	}
	if parseBoolScalar("false") || parseBoolScalar("") || parseBoolScalar("maybe") {
		t.Fatalf("falsy cases failed")
	}
}

func TestApplyMetaLine_ScalarAliases(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
		check func(t *testing.T, info SkillInfo)
	}{
		{
			name:  "name",
			key:   "name",
			value: ` "demo" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Name != "demo" {
					t.Fatalf("name = %q, want demo", info.Name)
				}
			},
		},
		{
			name:  "description",
			key:   "description",
			value: ` "short summary" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Description != "short summary" {
					t.Fatalf("description = %q", info.Description)
				}
			},
		},
		{
			name:  "digest alias writes summary",
			key:   "digest",
			value: ` "headline" `,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if info.Summary != "headline" {
					t.Fatalf("summary = %q", info.Summary)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info SkillInfo
			if used := applyMetaLine(&info, tt.key, tt.value, nil); used != 0 {
				t.Fatalf("used = %d, want 0", used)
			}
			tt.check(t, info)
		})
	}
}

func TestApplyMetaLine_ListAliasesConsumeTail(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		tail     []string
		wantUsed int
		check    func(t *testing.T, info SkillInfo)
	}{
		{
			name:     "keywords append trigger words",
			key:      "keywords",
			tail:     []string{"- alpha", "- beta", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.TriggerWords, []string{"alpha", "beta"}) {
					t.Fatalf("trigger words = %v", info.TriggerWords)
				}
			},
		},
		{
			name:     "must_words append force words",
			key:      "must_words",
			tail:     []string{"- gamma", "- delta", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.ForceWords, []string{"gamma", "delta"}) {
					t.Fatalf("force words = %v", info.ForceWords)
				}
			},
		},
		{
			name:     "tools append allowed tools",
			key:      "tools",
			tail:     []string{"- Read", "- Bash", "stop"},
			wantUsed: 2,
			check: func(t *testing.T, info SkillInfo) {
				t.Helper()
				if !reflect.DeepEqual(info.AllowedTools, []string{"Read", "Bash"}) {
					t.Fatalf("allowed tools = %v", info.AllowedTools)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var info SkillInfo
			used := applyMetaLine(&info, tt.key, "", tt.tail)
			if used != tt.wantUsed {
				t.Fatalf("used = %d, want %d", used, tt.wantUsed)
			}
			tt.check(t, info)
		})
	}
}

func TestApplyMetaLine_TrustAliases(t *testing.T) {
	info := SkillInfo{Trust: TrustUser}
	if used := applyMetaLine(&info, "trustscope", "banana", nil); used != 0 {
		t.Fatalf("used = %d, want 0", used)
	}
	if info.Trust != TrustUser {
		t.Fatalf("invalid trust should preserve existing value: %q", info.Trust)
	}
	applyMetaLine(&info, "trust_scope", "signed", nil)
	if info.Trust != TrustSigned {
		t.Fatalf("trust alias should set signed, got %q", info.Trust)
	}
}

func TestApplyMetaLine_DisableModelInvocationAliases(t *testing.T) {
	var info SkillInfo
	if used := applyMetaLine(&info, "disable_model_invocation", "yes", nil); used != 0 {
		t.Fatalf("used = %d, want 0", used)
	}
	if !info.DisableModelInvocation {
		t.Fatalf("disable model invocation should be true")
	}
}

type skillCatalogListStub struct {
	Service
	infos []SkillInfo
	err   error
}

func (s skillCatalogListStub) ListSkills(context.Context) ([]SkillInfo, error) {
	return s.infos, s.err
}

type skillCatalogRegistrarStub struct {
	registered []string
	err        error
}

func (s *skillCatalogRegistrarStub) RegisterDynamicProvider(provider contract.DynamicSectionProvider) error {
	if s.err != nil {
		return s.err
	}
	s.registered = append(s.registered, provider.SectionName())
	return nil
}

type skillCatalogInvalidatorStub struct {
	reason contract.InvalidateReason
	names  []string
	calls  int
}

func (s *skillCatalogInvalidatorStub) InvalidateSections(reason contract.InvalidateReason, names ...string) uint64 {
	s.reason = reason
	s.names = append([]string(nil), names...)
	s.calls++
	return uint64(s.calls)
}

func TestSkillCatalogProviderResolveRendersCoreAndIndex(t *testing.T) {
	provider := &SkillCatalogProvider{skills: skillCatalogListStub{infos: []SkillInfo{
		{Name: "juliet", Description: "desc juliet", Summary: "summary juliet"},
		{Name: "bravo", Description: "desc bravo", Summary: "summary bravo"},
		{Name: "hotel", Description: "desc hotel", Summary: "summary hotel"},
		{Name: "alpha", Description: "desc alpha", Summary: "summary alpha"},
		{Name: "echo", Description: "desc echo", Summary: "summary echo"},
		{Name: "golf", Description: "desc golf", Summary: "summary golf"},
		{Name: "india", Description: "desc india", Summary: "summary india"},
		{Name: "delta", Description: "desc delta", Summary: "summary delta"},
		{Name: "foxtrot", Description: "desc foxtrot", Summary: "summary foxtrot"},
		{Name: "charlie", Description: "desc charlie", Summary: "summary charlie"},
	}}}

	out, err := provider.Resolve(context.Background(), contract.SectionContext{})
	if err != nil || out == nil {
		t.Fatalf("Resolve() err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "## Core") || !strings.Contains(text, "## Index") {
		t.Fatalf("missing Core/Index sections: %q", text)
	}
	if !strings.Contains(text, "- alpha: desc alpha — summary alpha") {
		t.Fatalf("alpha core entry missing: %q", text)
	}
	if !strings.Contains(text, "india, juliet") {
		t.Fatalf("index should contain remaining names: %q", text)
	}
	if strings.Index(text, "- alpha:") > strings.Index(text, "- bravo:") {
		t.Fatalf("core entries should be sorted: %q", text)
	}
}

func TestSkillCatalogProviderResolveReturnsNilForEmptyList(t *testing.T) {
	provider := &SkillCatalogProvider{skills: skillCatalogListStub{infos: nil}}
	out, err := provider.Resolve(context.Background(), contract.SectionContext{})
	if err != nil || out != nil {
		t.Fatalf("Resolve() err=%v out=%v, want nil,nil", err, out)
	}
}

func TestSkillCatalogProviderBudgetTruncatesLargeCatalog(t *testing.T) {
	infos := make([]SkillInfo, 0, 60)
	for i := 59; i >= 0; i-- {
		infos = append(infos, SkillInfo{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: "description",
			Summary:     strings.Repeat("x", 24),
		})
	}
	provider := &SkillCatalogProvider{skills: skillCatalogListStub{infos: infos}, charBudget: 220}

	out, err := provider.Resolve(context.Background(), contract.SectionContext{})
	if err != nil || out == nil {
		t.Fatalf("Resolve() err=%v out=%v", err, out)
	}
	text := *out
	if len(text) > 220 {
		t.Fatalf("catalog length = %d, want <= 220: %q", len(text), text)
	}
	if !strings.Contains(text, "## Index") {
		t.Fatalf("truncated catalog should retain index: %q", text)
	}
	if !strings.Contains(text, "(+") {
		t.Fatalf("truncated catalog should advertise hidden entries: %q", text)
	}
}

func TestRegisterSkillCatalogPromptProviderRegistersSlot(t *testing.T) {
	registrar := &skillCatalogRegistrarStub{}
	if err := registerSkillCatalogPromptProvider(skillCatalogPromptProviderParams{
		Registrar: registrar,
		Provider:  NewSkillCatalogProvider(nil),
	}); err != nil {
		t.Fatalf("registerSkillCatalogPromptProvider() error = %v", err)
	}
	if !reflect.DeepEqual(registrar.registered, []string{contract.DynamicSectionSkillCatalog}) {
		t.Fatalf("registered = %v", registrar.registered)
	}
}

func TestSkillCatalogMutationsInvalidatePromptSection(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T, svc *service)
	}{
		{
			name: "write_local",
			run: func(t *testing.T, svc *service) {
				path := writeTestSkill(t, svc.root, "local-demo", "# before")
				if _, err := svc.WriteLocal(context.Background(), path, "# after"); err != nil {
					t.Fatalf("WriteLocal() error = %v", err)
				}
			},
		},
		{
			name: "import_local_dir",
			run: func(t *testing.T, svc *service) {
				sourceRoot := t.TempDir()
				sourceDir := filepath.Join(sourceRoot, "import-demo")
				writeTestSkill(t, sourceRoot, "import-demo", "# imported")
				if _, err := svc.ImportLocalDir(context.Background(), importSkillDirParams{Path: sourceDir}); err != nil {
					t.Fatalf("ImportLocalDir() error = %v", err)
				}
			},
		},
		{
			name: "delete_local",
			run: func(t *testing.T, svc *service) {
				writeTestSkill(t, svc.root, "delete-demo", "# delete")
				if _, err := svc.DeleteLocal(context.Background(), "delete-demo"); err != nil {
					t.Fatalf("DeleteLocal() error = %v", err)
				}
			},
		},
		{
			name: "write_remote",
			run: func(t *testing.T, svc *service) {
				if _, err := svc.WriteRemote(context.Background(), "remote-demo", "# remote"); err != nil {
					t.Fatalf("WriteRemote() error = %v", err)
				}
			},
		},
		{
			name: "write_skill_content",
			run: func(t *testing.T, svc *service) {
				if _, err := svc.WriteSkillContent(context.Background(), "config-demo", "# config"); err != nil {
					t.Fatalf("WriteSkillContent() error = %v", err)
				}
			},
		},
		{
			name: "write_summary",
			run: func(t *testing.T, svc *service) {
				writeTestSkill(t, svc.root, "summary-demo", "# summary")
				if _, err := svc.WriteSummary(context.Background(), "summary-demo", "updated summary"); err != nil {
					t.Fatalf("WriteSummary() error = %v", err)
				}
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			svc := newTestSkillService(t)
			invalidator := &skillCatalogInvalidatorStub{}
			svc.sections = invalidator
			tt.run(t, svc)
			if invalidator.calls != 1 {
				t.Fatalf("InvalidateSections() calls = %d, want 1", invalidator.calls)
			}
			if invalidator.reason != contract.InvalidateSkillCatalogWrite {
				t.Fatalf("InvalidateSections() reason = %q", invalidator.reason)
			}
			if !reflect.DeepEqual(invalidator.names, []string{contract.DynamicSectionSkillCatalog}) {
				t.Fatalf("InvalidateSections() names = %v", invalidator.names)
			}
		})
	}
}
