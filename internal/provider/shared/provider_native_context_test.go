package shared

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestBuildProviderNativeContextCanonicalizesCWDAndKeepsCapabilitiesExplicit(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "cwd-link")
	if err := os.Symlink(root, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	ctx, err := BuildProviderNativeContext(ProviderNativeContextInput{
		Provider:     " CLAUDE ",
		CWD:          link,
		ProviderHome: filepath.Join(root, "provider-home"),
		Profile:      "default",
		Capabilities: UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	if ctx.Provider != contract.ProviderNativeProviderClaude {
		t.Fatalf("Provider = %q, want %q", ctx.Provider, contract.ProviderNativeProviderClaude)
	}
	if ctx.CWD != root {
		t.Fatalf("CWD = %q, want canonical %q", ctx.CWD, root)
	}
	if ctx.Capabilities.Memory.Status != contract.ProviderNativeStatusUnknown ||
		ctx.Capabilities.Skills.Status != contract.ProviderNativeStatusUnknown {
		t.Fatalf("Capabilities = %#v, want explicit unknown statuses", ctx.Capabilities)
	}
	if err := ValidateProviderNativeContext(ctx); err != nil {
		t.Fatalf("ValidateProviderNativeContext() error = %v", err)
	}
	if err := ctx.RequireProviderFeature(contract.ProviderNativeFeatureSkills); !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("RequireProviderFeature(skills) error = %v, want explicit unknown", err)
	}
}

func TestBuildProviderNativeContextRejectsUnsafeCWDAndProvider(t *testing.T) {
	tests := []struct {
		name  string
		input ProviderNativeContextInput
	}{
		{name: "relative cwd", input: ProviderNativeContextInput{Provider: "codex", CWD: "relative"}},
		{name: "missing cwd", input: ProviderNativeContextInput{Provider: "codex"}},
		{name: "unknown provider", input: ProviderNativeContextInput{Provider: "other", CWD: t.TempDir()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := BuildProviderNativeContext(test.input); !errors.Is(err, contract.ErrProviderNativeContextInvalid) {
				t.Fatalf("BuildProviderNativeContext() error = %v, want context invalid", err)
			}
		})
	}
}

func TestBuildProviderNativeContextRejectsImplicitCapabilities(t *testing.T) {
	_, err := BuildProviderNativeContext(ProviderNativeContextInput{
		Provider: "claude",
		CWD:      t.TempDir(),
	})
	if !errors.Is(err, contract.ErrProviderNativeContextInvalid) {
		t.Fatalf("BuildProviderNativeContext() error = %v, want explicit capabilities error", err)
	}
}

func TestValidateProviderNativeContextRequiresOnlyExplicitFeatures(t *testing.T) {
	root := t.TempDir()
	capabilities := UnknownProviderNativeCapabilities()
	capabilities.Skills = contract.ProviderNativeFeatureCapability{
		Owner:    contract.ProviderNativeOwnerProvider,
		Status:   contract.ProviderNativeStatusSupported,
		Evidence: "probe:skills:v1",
	}
	ctx, err := BuildProviderNativeContext(ProviderNativeContextInput{
		Provider:     "codex",
		CWD:          root,
		Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	if err := ValidateProviderNativeContext(ctx, contract.ProviderNativeFeatureSkills); err != nil {
		t.Fatalf("ValidateProviderNativeContext(skills) error = %v", err)
	}
	if err := ValidateProviderNativeContext(ctx, contract.ProviderNativeFeatureMemory); !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("ValidateProviderNativeContext(memory) error = %v, want unknown", err)
	}
}

func TestValidateProviderNativeContextForProviderBindsOwner(t *testing.T) {
	ctx, err := BuildProviderNativeContext(ProviderNativeContextInput{
		Provider:     "claude",
		CWD:          t.TempDir(),
		Capabilities: UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	if err := ValidateProviderNativeContextForProvider(ctx, "claude"); err != nil {
		t.Fatalf("ValidateProviderNativeContextForProvider(claude) error = %v", err)
	}
	if err := ValidateProviderNativeContextForProvider(ctx, "codex"); !errors.Is(err, contract.ErrProviderNativeContextInvalid) {
		t.Fatalf("ValidateProviderNativeContextForProvider(codex) error = %v, want provider mismatch", err)
	}
}
