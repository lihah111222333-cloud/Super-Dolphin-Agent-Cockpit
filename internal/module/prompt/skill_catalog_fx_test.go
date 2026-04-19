package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// fakeSkillInjectionPort 最小实现 contract.SkillInjectionPort，供聚合 detector 测试。
type fakeSkillInjectionPort struct {
	names []string
}

func (f fakeSkillInjectionPort) DetectNativeSkills(_ string) []string { return f.names }
func (f fakeSkillInjectionPort) ReservedTokens() int                  { return 3000 }

// ---------------------------------------------------------------------------
// P20.1 Phase 10 Step B: compositeNativeSkillDetector 聚合测试
// ---------------------------------------------------------------------------

func TestCompositeNativeSkillDetector_UnionAndSorted(t *testing.T) {
	d := compositeNativeSkillDetector{
		ports: []contract.SkillInjectionPort{
			fakeSkillInjectionPort{names: []string{"zoo", "Alpha"}},
			fakeSkillInjectionPort{names: []string{"alpha", "bravo"}}, // "alpha" 去重
		},
	}
	got := d.DetectNativeSkills("/tmp")
	if len(got) != 3 {
		t.Fatalf("want 3 unique entries, got %d: %v", len(got), got)
	}
	// 字典序（case-insensitive）：Alpha, bravo, zoo
	if strings.ToLower(got[0]) != "alpha" || got[1] != "bravo" || got[2] != "zoo" {
		t.Fatalf("want [Alpha bravo zoo] case-insensitive sorted, got %v", got)
	}
}

func TestCompositeNativeSkillDetector_NilSafety(t *testing.T) {
	d := compositeNativeSkillDetector{ports: nil}
	if got := d.DetectNativeSkills("/tmp"); got != nil {
		t.Fatalf("empty composite: want nil, got %v", got)
	}
	d2 := compositeNativeSkillDetector{ports: []contract.SkillInjectionPort{nil, nil}}
	if got := d2.DetectNativeSkills(""); got != nil && len(got) > 0 {
		t.Fatalf("all-nil ports: want empty, got %v", got)
	}
}

func TestCompositeNativeSkillDetector_EmptyAndWhitespaceNames(t *testing.T) {
	d := compositeNativeSkillDetector{
		ports: []contract.SkillInjectionPort{
			fakeSkillInjectionPort{names: []string{"", "  ", "foo", " ", "foo"}},
		},
	}
	got := d.DetectNativeSkills("")
	if len(got) != 1 || got[0] != "foo" {
		t.Fatalf("want [foo], got %v", got)
	}
}

// ---------------------------------------------------------------------------
// P20.1 Phase 10 Step D: RegisterSkillCatalogProviderIfEnabled 灰度
// ---------------------------------------------------------------------------

// fakeDynamicRegistrar 记录每次 RegisterDynamicProvider 的调用，供灰度断言。
type fakeDynamicRegistrar struct {
	registered []string
	err        error
}

func (r *fakeDynamicRegistrar) RegisterDynamicProvider(p contract.DynamicSectionProvider) error {
	if r.err != nil {
		return r.err
	}
	r.registered = append(r.registered, p.SectionName())
	return nil
}

// fakeFxSkillLister 最小 skill.Service 替身（仅 ListSkills 被测试路径用到）。
// 与 skill_catalog_provider_test.go 的 fakeSkillLister 隔离命名，避免 duplicated declaration。
type fakeFxSkillLister struct{ skillpkg.Service }

func (fakeFxSkillLister) ListSkills(_ context.Context) ([]skillpkg.SkillInfo, error) {
	return nil, nil
}

func TestRegisterSkillCatalogProviderIfEnabled_FlagOff_Skips(t *testing.T) {
	reg := &fakeDynamicRegistrar{}
	cfg := &Config{EnableSkillProgressiveDisclosure: false}
	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  NewSkillCatalogProvider(nil, nil, 0),
		Skills:    fakeFxSkillLister{},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(reg.registered) != 0 {
		t.Fatalf("flag=off: want no registration, got %v", reg.registered)
	}
}

func TestRegisterSkillCatalogProviderIfEnabled_FlagOnSkillsNil_Skips(t *testing.T) {
	reg := &fakeDynamicRegistrar{}
	cfg := &Config{EnableSkillProgressiveDisclosure: true}
	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  NewSkillCatalogProvider(nil, nil, 0),
		Skills:    nil,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(reg.registered) != 0 {
		t.Fatalf("skills=nil: want no registration (fail-safe), got %v", reg.registered)
	}
}

func TestRegisterSkillCatalogProviderIfEnabled_Happy_Registers(t *testing.T) {
	reg := &fakeDynamicRegistrar{}
	cfg := &Config{
		EnableSkillProgressiveDisclosure: true,
		SkillCatalogTokenBudget:          3000,
		EmitSkillCatalogMetaInstructions: true,
	}
	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  NewSkillCatalogProvider(fakeFxSkillLister{}, nil, 12000),
		Skills:    fakeFxSkillLister{},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(reg.registered) != 1 || reg.registered[0] != DynamicSectionSkillCatalog {
		t.Fatalf("want [%s], got %v", DynamicSectionSkillCatalog, reg.registered)
	}
}

func TestRegisterSkillCatalogProviderIfEnabled_RegistrarErrorPropagates(t *testing.T) {
	reg := &fakeDynamicRegistrar{err: errors.New("boom")}
	cfg := &Config{EnableSkillProgressiveDisclosure: true}
	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  NewSkillCatalogProvider(fakeFxSkillLister{}, nil, 12000),
		Skills:    fakeFxSkillLister{},
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want wrapped registrar err, got %v", err)
	}
}
