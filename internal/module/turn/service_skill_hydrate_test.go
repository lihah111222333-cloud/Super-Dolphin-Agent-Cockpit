package turn

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// stubSkillLookup 是 turn.service 的 skillHydrationPort 测试替身。
type stubSkillLookup struct {
	infos      []contract.SkillInfo
	listErr    error
	bodies     map[string]string // absolute SKILL.md path → content
	readErrors map[string]error
	calls      struct {
		list      int
		readPaths []string
	}
}

func (s *stubSkillLookup) ListSkills(context.Context) ([]contract.SkillInfo, error) {
	s.calls.list++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.infos, nil
}

func (s *stubSkillLookup) ReadLocal(_ context.Context, path string) (any, error) {
	s.calls.readPaths = append(s.calls.readPaths, path)
	if err, ok := s.readErrors[path]; ok && err != nil {
		return nil, err
	}
	body, ok := s.bodies[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return map[string]any{
		"skill": map[string]any{
			"path":    path,
			"content": body,
			"summary": "",
		},
	}, nil
}

func TestPrepareTurnHydratesNameOnlySkillMetadataOnly(t *testing.T) {
	t.Parallel()

	dir := "/tmp/skills/debug"
	lookup := &stubSkillLookup{
		infos: []contract.SkillInfo{{
			Name:        "debug",
			Dir:         dir,
			Summary:     "debug helpers",
			Description: "Debug triggers",
			ContentHash: "0123456789abcdef0123456789abcdef",
			Trust:       contract.TrustUser,
		}},
		bodies: map[string]string{
			filepath.Join(dir, "SKILL.md"): "full debug body",
		},
	}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	session := &stubSession{threadID: "thread-hydrate"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "please investigate",
		Skills:               []dto.SkillRef{{Name: "debug", Source: dto.SkillSourceManual}},
		ManualSkillSelection: true,
		CWD:                  "/repo",
	})
	if err != nil {
		t.Fatalf("PrepareTurn error: %v", err)
	}
	if lookup.calls.list != 1 {
		t.Fatalf("ListSkills call count = %d, want 1", lookup.calls.list)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d: %+v", len(req.Skills), req.Skills)
	}
	got := req.Skills[0]
	if got.Prompt != "" {
		t.Fatalf("Prompt must not be hydrated from SKILL.md body, got %q", got.Prompt)
	}
	if got.Summary != "debug helpers" {
		t.Fatalf("Summary not hydrated: %q", got.Summary)
	}
	if got.Version != "0123456789ab" {
		t.Fatalf("Version should be short hash, got %q", got.Version)
	}
	if got.Source != dto.SkillSourceManual {
		t.Fatalf("Source should default to manual, got %q", got.Source)
	}
	if len(lookup.calls.readPaths) != 0 {
		t.Fatalf("ReadLocal must not be called for provider-native skill refs, got %v", lookup.calls.readPaths)
	}
}

func TestPrepareTurnPreservesSummaryWhenBodyMissing(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{
		infos: []contract.SkillInfo{{
			Name:        "rpc-tracing",
			Dir:         "/tmp/skills/rpc-tracing",
			Summary:     "trace bus/router flow",
			ContentHash: "deadbeefdeadbeefdeadbeef",
			Trust:       contract.TrustUser,
		}},
		// no bodies registered → ReadLocal returns error
		readErrors: map[string]error{
			filepath.Join("/tmp/skills/rpc-tracing", "SKILL.md"): errors.New("missing"),
		},
	}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	session := &stubSession{threadID: "thread-nobody"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "trace the event flow",
		Skills:               []dto.SkillRef{{Name: "rpc-tracing", Source: dto.SkillSourceManual}},
		ManualSkillSelection: true,
		CWD:                  "/repo",
	})
	if err != nil {
		t.Fatalf("PrepareTurn error: %v", err)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d: %+v", len(req.Skills), req.Skills)
	}
	got := req.Skills[0]
	if got.Prompt != "" {
		t.Fatalf("Prompt should stay empty when SKILL.md read fails, got %q", got.Prompt)
	}
	if got.Summary != "trace bus/router flow" {
		t.Fatalf("Summary should still be hydrated from SkillInfo, got %q", got.Summary)
	}
	if got.Version != "deadbeefdead" {
		t.Fatalf("Version should be short hash, got %q", got.Version)
	}
}

func TestPrepareTurnSkipsHydrateWhenLookupNil(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime())
	session := &stubSession{threadID: "thread-nil-lookup"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "hello",
		Skills:               []dto.SkillRef{{Name: "debug"}},
		ManualSkillSelection: true,
	})
	if err != nil {
		t.Fatalf("PrepareTurn error: %v", err)
	}
	if len(req.Skills) != 1 {
		t.Fatalf("want 1 skill, got %d", len(req.Skills))
	}
	if got := req.Skills[0]; got.Prompt != "" || got.Summary != "" || got.Version != "" {
		t.Fatalf("no lookup → no hydration, got %+v", got)
	}
}

func TestPrepareTurnSkipsHydrateWhenAlreadyPopulated(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{
		infos: []contract.SkillInfo{{
			Name:        "debug",
			Summary:     "should-not-override",
			ContentHash: "shouldnotoverride1",
		}},
	}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	session := &stubSession{threadID: "thread-nohit"}

	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:               "already full",
		Skills:               []dto.SkillRef{{Name: "debug", Prompt: "user body", Summary: "user summary", Version: "v1"}},
		ManualSkillSelection: true,
		CWD:                  "/repo",
	})
	if err != nil {
		t.Fatalf("PrepareTurn error: %v", err)
	}
	if lookup.calls.list != 0 {
		t.Fatalf("ListSkills should not be called when all fields populated, got %d", lookup.calls.list)
	}
	if got := req.Skills[0]; got.Prompt != "" || got.Summary != "user summary" || got.Version != "v1" {
		t.Fatalf("hydrate must not overwrite existing fields except stripping Prompt: %+v", got)
	}
}

func TestPrepareTurnStripsLegacyPromptForManualAndAutoMatchedSkills(t *testing.T) {
	t.Parallel()

	svc := NewServiceWithPromptAssembly(silentLogger(), &stubPromptAssemblyService{}, NewToolResultRuntime())
	session := &stubSession{threadID: "thread-strip-prompt"}
	req, err := svc.PrepareTurn(context.Background(), session, PrepareInput{
		Prompt:          "please use @debug on this issue",
		Skills:          []dto.SkillRef{{Name: "explicit", Prompt: "legacy explicit body", Summary: "explicit summary"}},
		CandidateSkills: []dto.SkillRef{{Name: "debug", Prompt: "legacy auto body", Summary: "debug summary"}},
	})
	if err != nil {
		t.Fatalf("PrepareTurn error: %v", err)
	}
	if len(req.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(req.Skills), req.Skills)
	}
	if got := req.Skills[0]; got.Name != "explicit" || got.Prompt != "" || got.Summary != "explicit summary" {
		t.Fatalf("explicit skill not normalized to metadata-only: %+v", got)
	}
	if got := req.Skills[1]; got.Name != "debug" || got.Prompt != "" || got.Summary != "debug summary" {
		t.Fatalf("auto-matched skill not normalized to metadata-only: %+v", got)
	}
}

func TestHydrateSkillRefsListSkillsErrorReturnsOriginal(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{listErr: errors.New("boom")}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	original := []dto.SkillRef{{Name: "debug"}}

	out, _ := svc.hydrateSkillRefs(contract.WithSkillCWD(context.Background(), "/repo"), original, false)
	if len(out) != 1 || out[0].Name != "debug" || out[0].Prompt != "" {
		t.Fatalf("ListSkills error must preserve input, got %+v", out)
	}
	if lookup.calls.list != 1 {
		t.Fatalf("ListSkills should be called once before giving up, got %d", lookup.calls.list)
	}
}

func TestHydrateSkillRefsSameNameConflictFailsClosed(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{listErr: contract.ErrSkillSameNameConflict}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	original := []dto.SkillRef{{Name: "debug"}}

	_, err := svc.hydrateSkillRefs(contract.WithSkillCWD(context.Background(), "/repo"), original, false)
	if !errors.Is(err, contract.ErrSkillSameNameConflict) {
		t.Fatalf("hydrateSkillRefs error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestHydrateSkillRefsCaseFoldedDuplicateFailsClosed(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{infos: []contract.SkillInfo{
		{Name: "Build", Dir: "/tmp/skills/Build", ContentHash: "1111111111111111"},
		{Name: "build", Dir: "/tmp/skills/build", ContentHash: "2222222222222222"},
	}}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	original := []dto.SkillRef{{Name: "build"}}

	_, err := svc.hydrateSkillRefs(contract.WithSkillCWD(context.Background(), "/repo"), original, false)
	if !errors.Is(err, contract.ErrSkillSameNameConflict) {
		t.Fatalf("hydrateSkillRefs duplicate error = %v, want ErrSkillSameNameConflict", err)
	}
}

func TestHydrateSkillRefsExactScopedSelectionAllowsSameName(t *testing.T) {
	t.Parallel()

	projectDir := "/repo/.agent/skills/docs"
	personalDir := "/home/skills/personal/user/docs"
	lookup := &stubSkillLookup{
		infos: []contract.SkillInfo{
			{Name: "docs", Scope: "project", Dir: projectDir, Summary: "project docs", ContentHash: "1111111111111111", Trust: contract.TrustProject},
			{Name: "docs", Scope: "personal", PersonalType: "user", Dir: personalDir, Summary: "personal docs", ContentHash: "2222222222222222", Trust: contract.TrustUser},
		},
		bodies: map[string]string{
			filepath.Join(personalDir, "SKILL.md"): "personal docs body",
		},
	}
	svc := newService(silentLogger(), &stubPromptAssemblyService{}, nil, lookup, nil, nil, nil, nil, NewToolResultRuntime()).(*service)
	original := []dto.SkillRef{{
		Name:         "docs",
		Scope:        "personal",
		PersonalType: "user",
		Path:         personalDir,
		Source:       dto.SkillSourceManual,
	}}

	out, err := svc.hydrateSkillRefs(contract.WithSkillCWD(context.Background(), "/repo"), original, true)
	if err != nil {
		t.Fatalf("hydrateSkillRefs exact scoped ref error = %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("hydrateSkillRefs len = %d, want 1: %+v", len(out), out)
	}
	got := out[0]
	if got.Summary != "personal docs" || got.Version != "222222222222" || got.Prompt != "" {
		t.Fatalf("exact scoped ref hydrated wrong skill: %+v", got)
	}
}
