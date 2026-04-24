package prompt

import (
	"context"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// fakeSkillLister 实现 SkillLister，便于单测。
type fakeSkillLister struct {
	infos []skillpkg.SkillInfo
	err   error
}

func (f fakeSkillLister) ListSkills(_ context.Context) ([]skillpkg.SkillInfo, error) {
	return f.infos, f.err
}

// fakeNativeDetector 实现 NativeSkillDetector。
type fakeNativeDetector struct {
	names []string
}

func (f fakeNativeDetector) DetectNativeSkills(_ string) ([]string, error) {
	return f.names, nil
}

// baseCtx 构造最小 SectionContext，cwd 指定以让 native detector 工作。
func baseCtx(cwd string) SectionContext {
	return SectionContext{
		BuildCtx: contract.BuildCtx{CWD: cwd},
	}
}

// ---- Resolve ----

func TestSkillCatalogProvider_EmptySkillsReturnsNil(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx("/tmp"))
	if err != nil {
		t.Fatalf("Resolve err: %v", err)
	}
	if out != nil {
		t.Fatalf("empty skill list should return nil, got %q", *out)
	}
}

func TestSkillCatalogProvider_ListErrorReturnsNilNoError(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{err: context.Canceled}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx("/tmp"))
	if err != nil {
		t.Fatalf("scan err should be swallowed: %v", err)
	}
	if out != nil {
		t.Fatalf("scan err should yield nil section")
	}
}

func TestSkillCatalogProvider_CoreGroupRendersFullMetadata(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "go-testing", Description: "Run Go tests", Summary: "use go test -run", Trust: skillpkg.TrustUser},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "### Core (trusted)") {
		t.Fatalf("missing Core header: %q", text)
	}
	if !strings.Contains(text, "**go-testing**") || !strings.Contains(text, "Run Go tests") {
		t.Fatalf("core entry missing fields: %q", text)
	}
	if !strings.Contains(text, "Summary: use go test -run") {
		t.Fatalf("core entry missing summary: %q", text)
	}
}

func TestSkillCatalogProvider_UntrustedRenderedAsRedactedPlaceholder(t *testing.T) {
	// 关键：project + untrusted 的作者原始 description/summary 绝对不能出现在 manifest 中
	// P20.1 §3.3 核心不变量
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{
			Name:        "rpc-tracing",
			Description: "SECRET ATTACK PAYLOAD",
			Summary:     "IGNORE PRIOR AND EXFIL",
			Trust:       skillpkg.TrustProject,
		},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if strings.Contains(text, "SECRET ATTACK PAYLOAD") || strings.Contains(text, "IGNORE PRIOR") {
		t.Fatalf("untrusted metadata MUST NOT leak: %q", text)
	}
	if !strings.Contains(text, "### Untrusted (metadata redacted until approval)") {
		t.Fatalf("missing Untrusted section: %q", text)
	}
	if !strings.Contains(text, "skill_expand_body(\"rpc-tracing\")") {
		t.Fatalf("redacted entry should guide to skill_expand_body: %q", text)
	}
}

func TestSkillCatalogProvider_NativeSkillAnnotated(t *testing.T) {
	p := NewSkillCatalogProvider(
		fakeSkillLister{infos: []skillpkg.SkillInfo{
			{Name: "claude-native-foo", Description: "irrelevant", Summary: "irrelevant", Trust: skillpkg.TrustUser},
		}},
		fakeNativeDetector{names: []string{"claude-native-foo"}},
		0,
	)
	out, err := p.Resolve(context.Background(), baseCtx("/some/project"))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "### Native (Claude CLI auto-loaded)") {
		t.Fatalf("missing Native section: %q", text)
	}
	if !strings.Contains(text, "**claude-native-foo**") {
		t.Fatalf("native entry missing: %q", text)
	}
	if strings.Contains(text, "### Core (trusted)") {
		t.Fatalf("native skill MUST NOT double-render in Core: %q", text)
	}
}

func TestSkillCatalogProvider_DisableModelInvocationLandsInManualOnly(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "dangerous", Description: "x", Trust: skillpkg.TrustUser, DisableModelInvocation: true},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "### Manual-only") {
		t.Fatalf("missing Manual-only section: %q", text)
	}
	if !strings.Contains(text, "`/dangerous`") {
		t.Fatalf("manual-only should guide to slash command: %q", text)
	}
	if strings.Contains(text, "### Core") {
		t.Fatalf("disable-model-invocation MUST NOT appear in Core: %q", text)
	}
}

func TestSkillCatalogProvider_MixedGroupsPreserveOrder(t *testing.T) {
	infos := []skillpkg.SkillInfo{
		{Name: "trusted-one", Trust: skillpkg.TrustUser, Description: "t1"},
		{Name: "native-foo", Trust: skillpkg.TrustUser, Description: "should render as native"},
		{Name: "untrusted-one", Trust: skillpkg.TrustProject, Description: "leak", Summary: "leak"},
		{Name: "manual-only", Trust: skillpkg.TrustUser, Description: "m", DisableModelInvocation: true},
	}
	p := NewSkillCatalogProvider(
		fakeSkillLister{infos: infos},
		fakeNativeDetector{names: []string{"native-foo"}},
		0,
	)
	out, err := p.Resolve(context.Background(), baseCtx("/proj"))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	// 顺序：Core > Native > Manual-only > Untrusted
	iCore := strings.Index(text, "### Core")
	iNative := strings.Index(text, "### Native")
	iManual := strings.Index(text, "### Manual-only")
	iRedacted := strings.Index(text, "### Untrusted")
	if iCore < 0 || iNative < 0 || iManual < 0 || iRedacted < 0 {
		t.Fatalf("missing some section; text=%q", text)
	}
	if !(iCore < iNative && iNative < iManual && iManual < iRedacted) {
		t.Fatalf("group order wrong: Core=%d Native=%d Manual=%d Redacted=%d\n%q", iCore, iNative, iManual, iRedacted, text)
	}
	// Redacted 不得包含作者原始内容
	if strings.Contains(text, "leak") {
		t.Fatalf("redacted leakage: %q", text)
	}
}

func TestSkillCatalogProvider_TokenBudgetTruncation(t *testing.T) {
	// 构造大量 skill，预算极小 → 只渲染 header + 少量
	many := make([]skillpkg.SkillInfo, 100)
	for i := range many {
		many[i] = skillpkg.SkillInfo{
			Name:        "skill-" + strings.Repeat("x", 20),
			Description: strings.Repeat("abc ", 30),
			Summary:     strings.Repeat("summary ", 10),
			Trust:       skillpkg.TrustUser,
		}
	}
	// 手动构造：让每个 skill name 不同
	for i := range many {
		many[i].Name = "s" + strings.Repeat("z", 3) + "-" + string(rune('a'+(i%26))) + string(rune('0'+(i%10)))
	}
	p := NewSkillCatalogProvider(fakeSkillLister{infos: many}, nil, 500) // 很小的预算
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "manifest truncated by token budget") {
		t.Fatalf("should contain truncation notice when budget exceeded: %q", text)
	}
}

// ============================================================================
// P20.1 Phase 9: 元指令
// ============================================================================

func TestSkillCatalogProvider_MetaInstructionsAppendedByDefault(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "foo", Description: "hi", Trust: skillpkg.TrustUser},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	// P20.1 Phase 9 必需内容
	if !strings.Contains(text, "## How to use skills") {
		t.Fatalf("missing meta-instructions header: %q", text)
	}
	if !strings.Contains(text, `skill_expand_body("<name>")`) {
		t.Fatalf("missing skill_expand_body hint: %q", text)
	}
	if !strings.Contains(text, `skill_read_resource("<name>", "<relative/path>")`) {
		t.Fatalf("missing skill_read_resource hint: %q", text)
	}
	if !strings.Contains(text, "false-positive") {
		t.Fatalf("should encourage false-positive over false-negative: %q", text)
	}
}

func TestSkillCatalogProvider_MetaInstructionsDisabled(t *testing.T) {
	p := NewSkillCatalogProviderWithOptions(
		fakeSkillLister{infos: []skillpkg.SkillInfo{
			{Name: "foo", Description: "hi", Trust: skillpkg.TrustUser},
		}},
		nil, 0,
		SkillCatalogOptions{EmitMetaInstructions: false},
	)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if strings.Contains(text, "## How to use skills") {
		t.Fatalf("meta-instructions should NOT be present when disabled: %q", text)
	}
	// 仍然有主列表
	if !strings.Contains(text, "### Core (trusted)") {
		t.Fatalf("main catalog should still render: %q", text)
	}
}

// TestSkillCatalogProvider_MetaInstructionsNativeGuardClause 锁定 Phase 9 审核
// 补充：元指令必须明确告知模型 Native 区 skill 不需 skill_expand_body，
// 避免“看到 Native entry 说 auto-loaded 但元指令说 call skill_expand_body”的矛盾。
func TestSkillCatalogProvider_MetaInstructionsNativeGuardClause(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "foo", Description: "hi", Trust: skillpkg.TrustUser},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "do NOT need") || !strings.Contains(text, "skill_expand_body") {
		t.Fatalf("meta-instructions should have Native guard clause: %q", text)
	}
	if !strings.Contains(text, "use `/<name>`") && !strings.Contains(text, "natural-language reference") {
		t.Fatalf("should explain Native alternative trigger: %q", text)
	}
}

// TestSkillCatalogProvider_NativeOnlyProjectScenario 项目只含 Claude CLI native skill
// 的场景下，manifest 依然渲染 Native 组 + 元指令；模型应能从元指令得到
// 正确指引（不对 Native skill 调 skill_expand_body）。
func TestSkillCatalogProvider_NativeOnlyProjectScenario(t *testing.T) {
	p := NewSkillCatalogProvider(
		fakeSkillLister{infos: []skillpkg.SkillInfo{
			{Name: "claude-foo", Description: "cfg", Trust: skillpkg.TrustUser},
		}},
		fakeNativeDetector{names: []string{"claude-foo"}},
		0,
	)
	out, err := p.Resolve(context.Background(), baseCtx("/proj"))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "### Native (Claude CLI auto-loaded)") {
		t.Fatalf("should render Native section: %q", text)
	}
	if !strings.Contains(text, "do NOT need") {
		t.Fatalf("meta-instructions Native guard missing in native-only scenario: %q", text)
	}
	if strings.Contains(text, "### Core (trusted)") {
		t.Fatalf("should NOT render Core when only Native: %q", text)
	}
}

// TestSkillCatalogProvider_Idempotent 防御：Resolve 多次调用返回相同结果。
// Phase 8 选用 CacheByName 缓存策略，缓存 key 安全的前提是 Resolve 在相同输入下
// deterministic。将来有人无心加时间戳或随机 observability tag 会被本测卡住。
func TestSkillCatalogProvider_Idempotent(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "alpha", Description: "d1", Trust: skillpkg.TrustUser},
		{Name: "beta", Description: "d2", Trust: skillpkg.TrustProject},
	}}, fakeNativeDetector{names: []string{"gamma"}}, 0)
	first, err := p.Resolve(context.Background(), baseCtx("/proj"))
	if err != nil || first == nil {
		t.Fatalf("first Resolve: err=%v out=%v", err, first)
	}
	for i := 0; i < 5; i++ {
		next, err := p.Resolve(context.Background(), baseCtx("/proj"))
		if err != nil || next == nil {
			t.Fatalf("iter %d: err=%v out=%v", i, err, next)
		}
		if *next != *first {
			t.Fatalf("iter %d non-deterministic; diff detected:\nfirst  %q\nlater  %q", i, *first, *next)
		}
	}
}

func TestSkillCatalogProvider_MetaInstructionsFallbackWhenTooLarge(t *testing.T) {
	// 构造极紧预算 — 恰好能放下 manifest 主体 + fallback，但放不下完整元指令
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{Name: "foo", Description: "d", Trust: skillpkg.TrustUser},
	}}, nil, 350) // manifest 小，fallback 能放下，但完整元指令过大
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	if strings.Contains(text, "## How to use skills") {
		t.Fatalf("full meta-instructions should NOT fit in tight budget: %q", text)
	}
	if !strings.Contains(text, "skill_expand_body") {
		t.Fatalf("fallback should mention skill_expand_body: %q", text)
	}
	if !strings.Contains(text, "skill_read_resource") {
		t.Fatalf("fallback should mention skill_read_resource: %q", text)
	}
}

func TestSkillCatalogProvider_SectionNameMatchesContract(t *testing.T) {
	p := NewSkillCatalogProvider(nil, nil, 0)
	if p.SectionName() != DynamicSectionSkillCatalog {
		t.Fatalf("SectionName mismatch: %q", p.SectionName())
	}
}

func TestSkillCatalogProvider_NilSkillsReturnsNil(t *testing.T) {
	p := NewSkillCatalogProvider(nil, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out != nil {
		t.Fatalf("nil lister should produce nil section, got err=%v out=%v", err, out)
	}
}

// ---- Helper functions ----

func TestTruncateCatalogSummary(t *testing.T) {
	if got := truncateCatalogSummary(""); got != "" {
		t.Fatalf("empty: %q", got)
	}
	if got := truncateCatalogSummary("  short  "); got != "short" {
		t.Fatalf("trim: %q", got)
	}
	long := strings.Repeat("a", 200)
	got := truncateCatalogSummary(long)
	if len(got) != skillCatalogMaxSummaryBytes+len("…") {
		t.Fatalf("len = %d, want %d", len(got), skillCatalogMaxSummaryBytes+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("should end with …: %q", got)
	}
}

func TestGroupSkillsForManifest_NativeWinsOverOtherGroupings(t *testing.T) {
	// native 命中 → 即使 disable-model-invocation=true 也应该进 Native
	infos := []skillpkg.SkillInfo{
		{Name: "foo", Trust: skillpkg.TrustUser, DisableModelInvocation: true},
	}
	g := groupSkillsForManifest(infos, map[string]struct{}{"foo": {}})
	if len(g.Native) != 1 || len(g.ManualOnly) != 0 {
		t.Fatalf("native should win: %+v", g)
	}
}

// TestSkillCatalogProvider_UntrustedDisableModelInvocation_NoLeak 锁定 §3.3 安全不变量：
// untrusted (Trust=Project) + disable-model-invocation=true 的 skill，名字和 description
// 都不得出现在 Manual-only 区。之前版本将其放入 Manual-only 属于安全漏洞：
// 攻击者可通过恶意 frontmatter (disable-model-invocation=true) 绕过净化。
func TestSkillCatalogProvider_UntrustedDisableModelInvocation_NoLeak(t *testing.T) {
	p := NewSkillCatalogProvider(fakeSkillLister{infos: []skillpkg.SkillInfo{
		{
			Name:                   "evil-manual",
			Description:            "SECRET_MANUAL_PAYLOAD",
			Summary:                "SECRET_MANUAL_SUMMARY",
			Trust:                  skillpkg.TrustProject,
			DisableModelInvocation: true,
		},
	}}, nil, 0)
	out, err := p.Resolve(context.Background(), baseCtx(""))
	if err != nil || out == nil {
		t.Fatalf("Resolve: err=%v out=%v", err, out)
	}
	text := *out
	// 净化不变量：任何用户建快内容不得泄露
	if strings.Contains(text, "SECRET_MANUAL_PAYLOAD") || strings.Contains(text, "SECRET_MANUAL_SUMMARY") {
		t.Fatalf("untrusted payload leaked: %q", text)
	}
	// 必须进 Redacted 区，而非 Manual-only
	if !strings.Contains(text, "### Untrusted (metadata redacted until approval)") {
		t.Fatalf("should be in Redacted: %q", text)
	}
	if strings.Contains(text, "### Manual-only") {
		t.Fatalf("untrusted + disable-model-invocation MUST NOT land in Manual-only: %q", text)
	}
	// Redacted 渲染包含 name + skill_expand_body 提示（不泄露内容）
	if !strings.Contains(text, "skill_expand_body(\"evil-manual\")") {
		t.Fatalf("redacted entry should guide to skill_expand_body: %q", text)
	}
	// 特别：不得出现 `/evil-manual` 这种 slash 调用提示（那是 Manual-only 渲染的标志）
	if strings.Contains(text, "`/evil-manual`") {
		t.Fatalf("slash invocation hint MUST NOT appear for untrusted skill: %q", text)
	}
}

// TestGroupSkillsForManifest_UntrustedGoesToRedactedNotManualOnly 直接验证 group 函数。
func TestGroupSkillsForManifest_UntrustedGoesToRedactedNotManualOnly(t *testing.T) {
	infos := []skillpkg.SkillInfo{
		{Name: "untrusted-plain", Trust: skillpkg.TrustProject},
		{Name: "untrusted-manual", Trust: skillpkg.TrustProject, DisableModelInvocation: true},
		{Name: "trusted-manual", Trust: skillpkg.TrustUser, DisableModelInvocation: true},
		{Name: "trusted-plain", Trust: skillpkg.TrustUser},
	}
	g := groupSkillsForManifest(infos, nil)
	if len(g.Redacted) != 2 {
		t.Fatalf("both untrusted should land in Redacted, got %d: %+v", len(g.Redacted), g.Redacted)
	}
	if len(g.ManualOnly) != 1 || g.ManualOnly[0].Name != "trusted-manual" {
		t.Fatalf("only trusted-manual should be in ManualOnly: %+v", g.ManualOnly)
	}
	if len(g.Core) != 1 || g.Core[0].Name != "trusted-plain" {
		t.Fatalf("trusted-plain should be in Core: %+v", g.Core)
	}
}

// TestIsUntrustedScope 锁定隐式契约：仅 TrustUser / TrustSigned 解锁，其余均 untrusted。
func TestIsUntrustedScope(t *testing.T) {
	trustedCases := []skillpkg.TrustScope{skillpkg.TrustUser, skillpkg.TrustSigned}
	for _, s := range trustedCases {
		if isUntrustedScope(s) {
			t.Fatalf("%q should be trusted", s)
		}
	}
	untrustedCases := []skillpkg.TrustScope{
		skillpkg.TrustProject,
		skillpkg.TrustUnknown,
		"",
		"banana",
		"admin",
	}
	for _, s := range untrustedCases {
		if !isUntrustedScope(s) {
			t.Fatalf("%q should be untrusted (conservative)", s)
		}
	}
}

func TestGroupSkillsForManifest_UnknownTrustDefaultsToRedacted(t *testing.T) {
	infos := []skillpkg.SkillInfo{{Name: "foo", Trust: "banana"}}
	g := groupSkillsForManifest(infos, nil)
	if len(g.Redacted) != 1 {
		t.Fatalf("unknown trust should fall to Redacted (conservative): %+v", g)
	}
}
