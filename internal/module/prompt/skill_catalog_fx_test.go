package prompt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// ---------------------------------------------------------------------------
// P20.1 Phase 10 Step D: RegisterSkillCatalogProviderIfEnabled 灰度
// ---------------------------------------------------------------------------

func TestPromptConfigDefaultsProgressiveDisclosureOn(t *testing.T) {
	t.Setenv(envEnableSkillProgressiveDisclosure, "")
	cfg := NewConfig(nil)
	if !cfg.EnableSkillProgressiveDisclosure {
		t.Fatal("EnableSkillProgressiveDisclosure default = false, want true (P25 Phase 4 close)")
	}
}

func TestSkillProgressiveDisclosure_DefaultEnabled(t *testing.T) {
	t.Setenv(envEnableSkillProgressiveDisclosure, "")
	cfg := NewConfig(nil)
	reg := &fakeDynamicRegistrar{}
	provider := NewSkillCatalogProvider(fakeFxSkillLister{}, nil, 0)

	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  provider,
		Skills:    fakeFxSkillLister{},
	})
	if err != nil {
		t.Fatalf("RegisterSkillCatalogProviderIfEnabled() error = %v", err)
	}
	if len(reg.registered) != 1 || reg.registered[0] != DynamicSectionSkillCatalog {
		t.Fatalf("default enabled (P25 Phase 4): want skill catalog registered once, got %v", reg.registered)
	}
}

func TestSkillProgressiveDisclosure_EnableFlagRendersCatalog(t *testing.T) {
	t.Setenv(envEnableSkillProgressiveDisclosure, "true")
	cfg := NewConfig(nil)
	reg := &fakeDynamicRegistrar{}
	provider := NewSkillCatalogProvider(
		fakeSkillLister{infos: []skillpkg.SkillInfo{{
			Name:        "demo-skill",
			Description: "visible description",
			Summary:     "visible summary",
			Trust:       skillpkg.TrustUser,
		}}},
		nil,
		0,
	)

	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: reg,
		Provider:  provider,
		Skills:    fakeFxSkillLister{},
	})
	if err != nil {
		t.Fatalf("RegisterSkillCatalogProviderIfEnabled() error = %v", err)
	}
	if len(reg.registered) != 1 || reg.registered[0] != DynamicSectionSkillCatalog {
		t.Fatalf("enabled: want [%s], got %v", DynamicSectionSkillCatalog, reg.registered)
	}

	out, err := provider.Resolve(context.Background(), baseCtx("/repo"))
	if err != nil || out == nil {
		t.Fatalf("Resolve() err=%v out=%v", err, out)
	}
	text := *out
	if !strings.Contains(text, "### Core") || !strings.Contains(text, "visible description") || !strings.Contains(text, "visible summary") {
		t.Fatalf("enabled catalog should render trusted skill metadata, got %q", text)
	}
}

func TestSkillProgressiveDisclosure_EnableFlagRegistersIntoAssembler(t *testing.T) {
	cfg := &Config{EnableSkillProgressiveDisclosure: true, EmitSkillCatalogMetaInstructions: false}
	svc := NewService(cfg, nil)
	provider := NewSkillCatalogProviderWithOptions(
		fakeSkillLister{infos: []skillpkg.SkillInfo{{
			Name:        "demo-skill",
			Description: "visible description",
			Summary:     "visible summary",
			Trust:       skillpkg.TrustUser,
		}}},
		nil,
		12000,
		SkillCatalogOptions{EmitMetaInstructions: false},
	)

	err := RegisterSkillCatalogProviderIfEnabled(registerSkillCatalogDeps{
		Cfg:       cfg,
		Registrar: svc,
		Provider:  provider,
		Skills:    fakeFxSkillLister{},
	})
	if err != nil {
		t.Fatalf("RegisterSkillCatalogProviderIfEnabled() error = %v", err)
	}
	assembly, err := svc.AssembleStart(context.Background(), StartInput{
		BaseInstructions: "legacy base",
		Provider:         "codex",
		CWD:              "/repo",
	})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	for _, want := range []string{"legacy base", "demo-skill", "visible description", "visible summary"} {
		if !strings.Contains(assembly.BaseInstructions, want) {
			t.Fatalf("BaseInstructions missing %q after enabled registration:\n%s", want, assembly.BaseInstructions)
		}
	}
}

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

// fakeFxSkillLister 最小 skill catalog source 替身。
// 与 skill_catalog_provider_test.go 的 fakeSkillLister 隔离命名，避免 duplicated declaration。
type fakeFxSkillLister struct{}

func (fakeFxSkillLister) ListSkills(_ context.Context) ([]skillpkg.SkillInfo, error) {
	return nil, nil
}

func (fakeFxSkillLister) LookupArtifactApproval(context.Context, contract.ArtifactApprovalRequest) (bool, error) {
	return false, nil
}

func (fakeFxSkillLister) ApprovalRevision() uint64 { return 0 }
func (fakeFxSkillLister) SkillRevision() uint64    { return 0 }
func (fakeFxSkillLister) TrustRevision() uint64    { return 0 }

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
		Skills:    nil,
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want wrapped registrar err, got %v", err)
	}
}
