package codexapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestCodexProviderNativeSkillsConfigRejectsUnsupportedDisableFlags(t *testing.T) {
	t.Parallel()

	for _, cfg := range []map[string]any{
		{"providerNativeSkills": false},
		{"provider_native_skills": false},
		{"disableProviderNativeSkills": true},
		{"disable_provider_native_skills": true},
		{"providerNativeSkills": false, "disableProviderNativeSkills": true},
	} {
		err := validateCodexProviderNativeSkillsConfig(cfg)
		if !errors.Is(err, contract.ErrProviderNativeCapabilityUnsupported) {
			t.Fatalf("validateCodexProviderNativeSkillsConfig(%#v) error = %v, want unsupported capability", cfg, err)
		}
	}
}

func TestCodexProviderNativeSkillsConfigRejectsAmbiguousAliases(t *testing.T) {
	t.Parallel()

	for _, cfg := range []map[string]any{
		{"providerNativeSkills": true, "provider_native_skills": false},
		{"disableProviderNativeSkills": true, "disable_provider_native_skills": false},
		{"providerNativeSkills": true, "disableProviderNativeSkills": true},
		{"providerNativeSkills": false, "disableProviderNativeSkills": false},
	} {
		err := validateCodexProviderNativeSkillsConfig(cfg)
		if !errors.Is(err, contract.ErrProviderNativeContextInvalid) {
			t.Fatalf("validateCodexProviderNativeSkillsConfig(%#v) error = %v, want invalid context", cfg, err)
		}
		if !strings.Contains(err.Error(), "conflict") {
			t.Fatalf("validateCodexProviderNativeSkillsConfig(%#v) error = %v, want conflict diagnostic", cfg, err)
		}
	}
}

func TestCodexProviderNativeSkillsConfigAllowsEnableFlags(t *testing.T) {
	t.Parallel()

	for _, cfg := range []map[string]any{
		{"providerNativeSkills": true},
		{"provider_native_skills": true},
		{"disableProviderNativeSkills": false},
		{"disable_provider_native_skills": false},
		{"providerNativeSkills": true, "provider_native_skills": true},
		{"disableProviderNativeSkills": false, "disable_provider_native_skills": false},
		{"providerNativeSkills": true, "disableProviderNativeSkills": false},
	} {
		if err := validateCodexProviderNativeSkillsConfig(cfg); err != nil {
			t.Fatalf("validateCodexProviderNativeSkillsConfig(%#v) error = %v, want nil", cfg, err)
		}
	}
}

func TestCodexStartAndResumeBoundariesRejectUnsupportedProviderNativeSkillsFlags(t *testing.T) {
	t.Parallel()

	d := &driver{}
	startReq := dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-skills-disabled",
		CWD:      t.TempDir(),
		Config:   map[string]any{"disableProviderNativeSkills": true},
	}
	if _, err := d.prepareStartSessionRequest(context.Background(), startReq); !errors.Is(err, contract.ErrProviderNativeCapabilityUnsupported) {
		t.Fatalf("prepareStartSessionRequest() error = %v, want unsupported capability", err)
	}
	resumeReq := dto.ResumeSessionRequest{
		Provider:         "codex",
		AgentID:          "agent-skills-disabled-resume",
		ProviderThreadID: "provider-thread",
		CWD:              t.TempDir(),
		Config:           map[string]any{"providerNativeSkills": false},
	}
	if _, err := d.ResolveResumeSessionIdentity(context.Background(), resumeReq); !errors.Is(err, contract.ErrProviderNativeCapabilityUnsupported) {
		t.Fatalf("ResolveResumeSessionIdentity() error = %v, want unsupported capability", err)
	}
}

func TestCodexProviderNativeSkillsConfigRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	err := validateCodexProviderNativeSkillsConfig(map[string]any{"providerNativeSkills": "true"})
	if !errors.Is(err, contract.ErrProviderNativeContextInvalid) || !strings.Contains(err.Error(), "providerNativeSkills") {
		t.Fatalf("validateCodexProviderNativeSkillsConfig() error = %v, want invalid providerNativeSkills", err)
	}
}

func TestCodexExplicitSkillsFailClosedWhenCapabilityUnknown(t *testing.T) {
	t.Parallel()

	ctx, err := providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "codex",
		CWD:          t.TempDir(),
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	s := &session{}
	s.setNativeContext(ctx)
	err = s.requireProviderNativeSkills([]dto.SkillRef{{Name: "review"}})
	if !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("requireProviderNativeSkills() error = %v, want unknown capability", err)
	}
}

func TestCodexPrepareTurnStartParamsRejectsExplicitSkillsWhenCapabilityUnknown(t *testing.T) {
	t.Parallel()

	ctx, err := providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "codex",
		CWD:          t.TempDir(),
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	s := &session{}
	s.threadID.Store("thread-native-skills")
	s.setNativeContext(ctx)
	_, err = s.prepareTurnStartParams(context.Background(), dto.TurnRequest{
		Inputs: []dto.InputItem{{Content: "hello"}},
		Skills: []dto.SkillRef{{Name: "review"}},
	})
	if !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("prepareTurnStartParams() error = %v, want unknown capability", err)
	}
}

func TestCodexLegacySessionWithoutNativeContextKeepsDirectSkillCompatibility(t *testing.T) {
	t.Parallel()

	if err := (&session{}).requireProviderNativeSkills([]dto.SkillRef{{Name: "review"}}); err != nil {
		t.Fatalf("requireProviderNativeSkills() error = %v, want nil for schema-zero legacy session", err)
	}
}
