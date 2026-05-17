package prompt

import (
	"context"
	"strings"
	"testing"
)

func TestLanguageProviderResolveBuildsLanguagePrompt(t *testing.T) {
	provider := LanguageProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{BuildCtx: BuildCtx{Language: "Chinese"}})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want content")
	}
	want := "# Language\nAlways respond in Chinese. Use Chinese for all explanations, comments, and communications with the user. Technical terms and code identifiers should remain in their original form."
	if *text != want {
		t.Fatalf("Resolve() = %q, want %q", *text, want)
	}
}

func TestLanguageProviderResolveUsesDefaultForEmptyLanguage(t *testing.T) {
	provider := LanguageProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text == nil {
		t.Fatal("Resolve() = nil, want default language section")
	}
	if *text != languageDefaultSectionText {
		t.Fatalf("Resolve() = %q, want default language section %q", *text, languageDefaultSectionText)
	}
	if !strings.Contains(*text, "Do not mix languages") {
		t.Fatalf("default language section missing anti-mixing anchor: %q", *text)
	}
	if !strings.Contains(*text, "first user message") {
		t.Fatalf("default language section missing first-message fallback: %q", *text)
	}
}

func TestAssembleStartEmptyLanguageStillAnchorsLanguage(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleStart(context.Background(), StartInput{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("AssembleStart() error = %v", err)
	}
	language, ok := resolvedSectionContent(assembly.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("AssembleStart() missing %q section for empty language", DynamicSectionLanguage)
	}
	if !strings.Contains(language, "Do not mix languages") {
		t.Fatalf("AssembleStart() language section = %q, want default anti-mixing anchor", language)
	}
}

func TestAssembleTurnEmptyLanguageStillAnchorsLanguage(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleTurn(context.Background(), TurnInput{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("AssembleTurn() error = %v", err)
	}
	language, ok := resolvedSectionContent(assembly.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("AssembleTurn() missing %q section for empty language", DynamicSectionLanguage)
	}
	if !strings.Contains(language, "Do not mix languages") {
		t.Fatalf("AssembleTurn() language section = %q, want default anti-mixing anchor", language)
	}
}

func TestAssembleAgentEmptyLanguageStillAnchorsLanguage(t *testing.T) {
	t.Setenv(envClaudeSimple, "")
	svc := NewService(&Config{}, nil)
	assembly, err := svc.AssembleAgent(context.Background(), AgentInput{
		StartInput: StartInput{CWD: t.TempDir()},
		AgentType:  AgentTypeDefault,
	})
	if err != nil {
		t.Fatalf("AssembleAgent() error = %v", err)
	}
	language, ok := resolvedSectionContent(assembly.ResolvedSections, DynamicSectionLanguage)
	if !ok {
		t.Fatalf("AssembleAgent() missing %q section for empty language", DynamicSectionLanguage)
	}
	if !strings.Contains(language, "Do not mix languages") {
		t.Fatalf("AssembleAgent() language section = %q, want default anti-mixing anchor", language)
	}
}
