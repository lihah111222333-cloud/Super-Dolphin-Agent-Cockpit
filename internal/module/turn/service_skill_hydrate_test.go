package turn

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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

func TestPrepareTurnHydratesNameOnlySkill(t *testing.T) {
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
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
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
	if got.Prompt != "full debug body" {
		t.Fatalf("Prompt not hydrated: %q", got.Prompt)
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
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
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

	svc := NewService(silentLogger())
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
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
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
	if got := req.Skills[0]; got.Prompt != "user body" || got.Summary != "user summary" || got.Version != "v1" {
		t.Fatalf("hydrate must not overwrite existing fields: %+v", got)
	}
}

func TestHydrateSkillRefsListSkillsErrorReturnsOriginal(t *testing.T) {
	t.Parallel()

	lookup := &stubSkillLookup{listErr: errors.New("boom")}
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
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
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
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
	svc := newService(silentLogger(), nil, nil, lookup, nil, nil, nil).(*service)
	original := []dto.SkillRef{{Name: "build"}}

	_, err := svc.hydrateSkillRefs(contract.WithSkillCWD(context.Background(), "/repo"), original, false)
	if !errors.Is(err, contract.ErrSkillSameNameConflict) {
		t.Fatalf("hydrateSkillRefs duplicate error = %v, want ErrSkillSameNameConflict", err)
	}
}
