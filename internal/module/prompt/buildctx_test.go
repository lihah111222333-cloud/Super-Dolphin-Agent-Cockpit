package prompt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
)

type stubClaudeMdSourceProvider struct {
	calls int
	err   error
}

func (p *stubClaudeMdSourceProvider) ResolveClaudeMdSources(context.Context, BuildCtx) ([]contract.ClaudeMdSource, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return []contract.ClaudeMdSource{{
		Path:    "/repo/CLAUDE.md",
		Type:    "project",
		Content: fmt.Sprintf("version-%d", p.calls),
		Digest:  "stable-digest",
	}}, nil
}

func TestBuildBaseUserContextSkipsConditionalAndWrapsTeamMemory(t *testing.T) {
	base := BuildBaseUserContext([]contract.ClaudeMdSource{
		{Path: "/repo/CLAUDE.md", Type: "project", Content: "Project instructions", Digest: "project-digest"},
		{Path: "/repo/.claude/rules/path-specific.md", Type: "project", Content: "Conditional instructions", Conditional: true, Digest: "conditional-digest"},
		{Path: "/team/MEMORY.md", Type: "teammem", Content: "Shared team memory", Digest: "team-digest"},
	})
	merged := MergeRuntimeUserContext(base, map[string]string{
		"currentDate": "Today's date is 2026-04-15.",
	})
	text := contract.RenderUserContextMessage(dto.TurnAssembly{UserContext: merged})
	for _, check := range []string{
		"<system-reminder>",
		"# claudeMd",
		"Contents of /repo/CLAUDE.md:",
		// project 来源来自仓库内容，必须包在 untrusted fence 内，防止被当成系统指令执行。
		"<untrusted-claude-md>",
		"</untrusted-claude-md>",
		"<team-memory-content source=\"shared\">",
		"Shared team memory",
		"# currentDate",
	} {
		if !strings.Contains(text, check) {
			t.Fatalf("RenderUserContextMessage = %q, want substring %q", text, check)
		}
	}
	if strings.Contains(text, "Conditional instructions") {
		t.Fatalf("RenderUserContextMessage = %q, want conditional rule omitted", text)
	}
}

func TestAssembleTurnFailsOnClaudeMdSourceError(t *testing.T) {
	svc := NewService(&Config{}, nil)
	provider := &stubClaudeMdSourceProvider{err: fmt.Errorf("nested rule markdown stat: permission denied")}
	if err := svc.RegisterClaudeMdSourceProvider(provider); err != nil {
		t.Fatalf("RegisterClaudeMdSourceProvider() error = %v", err)
	}
	if _, err := svc.AssembleTurn(context.Background(), TurnInput{}); err == nil {
		t.Fatal("AssembleTurn() error = nil, want ClaudeMd source error")
	}
}

func TestAssembleStartFailsOnClaudeMdSourceContainmentError(t *testing.T) {
	svc := NewService(&Config{}, nil)
	provider := &stubClaudeMdSourceProvider{
		err: fmt.Errorf("ClaudeMd candidate containment %q under %q: %w", "/tmp/outside/CLAUDE.md", "/tmp/project", memshared.ErrSafeReadContainment),
	}
	if err := svc.RegisterClaudeMdSourceProvider(provider); err != nil {
		t.Fatalf("RegisterClaudeMdSourceProvider() error = %v", err)
	}

	_, err := svc.AssembleStart(context.Background(), StartInput{CWD: "/tmp/project", Provider: "claude"})
	if err == nil {
		t.Fatal("AssembleStart() error = nil, want ClaudeMd containment error")
	}
	if !errors.Is(err, memshared.ErrSafeReadContainment) {
		t.Fatalf("AssembleStart() error = %v, want ErrSafeReadContainment", err)
	}
	if !strings.Contains(err.Error(), "ClaudeMd") || !strings.Contains(err.Error(), "safe read") {
		t.Fatalf("AssembleStart() error = %v, want ClaudeMd safe read context", err)
	}
}

func TestAssembleTurnCachesBaseUserContextWithoutCachingCurrentDate(t *testing.T) {
	svc := NewService(&Config{}, nil)
	provider := &stubClaudeMdSourceProvider{}
	if err := svc.RegisterClaudeMdSourceProvider(provider); err != nil {
		t.Fatalf("RegisterClaudeMdSourceProvider() error = %v", err)
	}

	first, err := svc.AssembleTurn(context.Background(), TurnInput{CurrentDate: "2026-04-15"})
	if err != nil {
		t.Fatalf("first AssembleTurn() error = %v", err)
	}
	second, err := svc.AssembleTurn(context.Background(), TurnInput{CurrentDate: "2026-04-16"})
	if err != nil {
		t.Fatalf("second AssembleTurn() error = %v", err)
	}
	if !strings.Contains(first.UserContextText, "version-1") {
		t.Fatalf("first UserContextText = %q, want first base render", first.UserContextText)
	}
	if strings.Contains(second.UserContextText, "version-2") {
		t.Fatalf("second UserContextText = %q, want cached base payload reused", second.UserContextText)
	}
	if !strings.Contains(second.UserContextText, "Today's date is 2026-04-16.") {
		t.Fatalf("second UserContextText = %q, want fresh current date", second.UserContextText)
	}

	if err := svc.Invalidate(context.Background(), InvalidateClear); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	third, err := svc.AssembleTurn(context.Background(), TurnInput{CurrentDate: "2026-04-17"})
	if err != nil {
		t.Fatalf("third AssembleTurn() error = %v", err)
	}
	if !strings.Contains(third.UserContextText, "version-3") {
		t.Fatalf("third UserContextText = %q, want rebuilt base payload after invalidate", third.UserContextText)
	}
}
