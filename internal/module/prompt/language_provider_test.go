package prompt

import (
	"context"
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

func TestLanguageProviderResolveSkipsEmptyLanguage(t *testing.T) {
	provider := LanguageProvider{}
	text, err := provider.Resolve(context.Background(), SectionContext{})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if text != nil {
		t.Fatalf("Resolve() = %q, want nil", *text)
	}
}
