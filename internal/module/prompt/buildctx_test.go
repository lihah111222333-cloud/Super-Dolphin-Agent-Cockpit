package prompt

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type stubClaudeMdSourceProvider struct {
	calls int
}

func (p *stubClaudeMdSourceProvider) ResolveClaudeMdSources(context.Context, BuildCtx) []contract.ClaudeMdSource {
	p.calls++
	return []contract.ClaudeMdSource{{
		Path:    "/repo/CLAUDE.md",
		Type:    "project",
		Content: fmt.Sprintf("version-%d", p.calls),
		Digest:  "stable-digest",
	}}
}

func TestBuildBaseUserContextSkipsConditionalAndWrapsTeamMemory(t *testing.T) {
	base := BuildBaseUserContext([]contract.ClaudeMdSource{
		{Path: "/repo/CLAUDE.md", Type: "project", Content: "Project instructions", Digest: "project-digest"},
		{Path: "/repo/.claude/rules/path-specific.md", Type: "project", Content: "Conditional instructions", Conditional: true, Digest: "conditional-digest"},
		{Path: "/team/MEMORY.md", Type: "teammem", Content: "Shared team memory", Digest: "team-digest"},
	})
	text := FormatUserContextMessage(MergeRuntimeUserContext(base, map[string]string{
		"currentDate": "Today's date is 2026-04-15.",
		"disclaimer":  runtimeExtrasRelevanceDisclaimer,
	}))
	for _, check := range []string{
		"<system-reminder>",
		"# claudeMd",
		"Contents of /repo/CLAUDE.md:",
		"<team-memory-content source=\"shared\">",
		"Shared team memory",
		"# currentDate",
	} {
		if !strings.Contains(text, check) {
			t.Fatalf("FormatUserContextMessage() = %q, want substring %q", text, check)
		}
	}
	if strings.Contains(text, "Conditional instructions") {
		t.Fatalf("FormatUserContextMessage() = %q, want conditional rule omitted", text)
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
