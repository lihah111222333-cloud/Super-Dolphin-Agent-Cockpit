package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestClaudeProviderNativeSkillsConfigRejectsAmbiguousAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{
			name: "positive aliases disagree",
			cfg:  map[string]any{"providerNativeSkills": true, "provider_native_skills": false},
			want: "providerNativeSkills aliases conflict",
		},
		{
			name: "negative aliases disagree",
			cfg:  map[string]any{"disableProviderNativeSkills": true, "disable_provider_native_skills": false},
			want: "disableProviderNativeSkills aliases conflict",
		},
		{
			name: "positive and negative both enabled",
			cfg:  map[string]any{"providerNativeSkills": true, "disableProviderNativeSkills": true},
			want: "provider native skills flags conflict",
		},
		{
			name: "positive and negative both disabled",
			cfg:  map[string]any{"providerNativeSkills": false, "disableProviderNativeSkills": false},
			want: "provider native skills flags conflict",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClaudeSecurityConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validateClaudeSecurityConfig(%#v) error = %v, want %q", tt.cfg, err, tt.want)
			}
		})
	}
}

func TestClaudeProviderNativeSkillsConfigAllowsEquivalentAliases(t *testing.T) {
	t.Parallel()

	for _, cfg := range []map[string]any{
		{"providerNativeSkills": true, "provider_native_skills": true},
		{"disableProviderNativeSkills": true, "disable_provider_native_skills": true},
		{"providerNativeSkills": true, "disableProviderNativeSkills": false},
		{"providerNativeSkills": false, "disableProviderNativeSkills": true},
	} {
		if err := validateClaudeSecurityConfig(cfg); err != nil {
			t.Fatalf("validateClaudeSecurityConfig(%#v) error = %v, want nil", cfg, err)
		}
	}
}

func TestClaudeStartSessionRejectsConflictingProviderNativeSkillsAliasesBeforeLaunch(t *testing.T) {
	t.Parallel()

	d := &driver{}
	_, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "claude",
		AgentID:  "agent-skills-conflict",
		CWD:      t.TempDir(),
		Config: map[string]any{
			"providerNativeSkills":        true,
			"disableProviderNativeSkills": true,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "provider native skills flags conflict") {
		t.Fatalf("StartSession() error = %v, want fail-fast skills flag conflict", err)
	}
}

func TestClaudeExplicitSkillsFailClosedWhenCapabilityUnknown(t *testing.T) {
	t.Parallel()

	ctx, err := providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "claude",
		CWD:          t.TempDir(),
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	s := &session{nativeContext: ctx}
	s.mu.Lock()
	err = s.requireProviderNativeSkillsLocked([]dto.SkillRef{{Name: "review"}})
	s.mu.Unlock()
	if !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("requireProviderNativeSkillsLocked() error = %v, want unknown capability", err)
	}
}

func TestClaudePrepareTurnBlocksRejectsExplicitSkillsWhenCapabilityUnknown(t *testing.T) {
	t.Parallel()

	ctx, err := providershared.BuildProviderNativeContext(providershared.ProviderNativeContextInput{
		Provider:     "claude",
		CWD:          t.TempDir(),
		Capabilities: providershared.UnknownProviderNativeCapabilities(),
	})
	if err != nil {
		t.Fatalf("BuildProviderNativeContext() error = %v", err)
	}
	s := &session{nativeContext: ctx}
	s.mu.Lock()
	_, err = s.prepareTurnBlocksLocked(dto.TurnRequest{
		Inputs: []dto.InputItem{{Content: "hello"}},
		Skills: []dto.SkillRef{{Name: "review"}},
	})
	s.mu.Unlock()
	if !errors.Is(err, contract.ErrProviderNativeCapabilityUnknown) {
		t.Fatalf("prepareTurnBlocksLocked() error = %v, want unknown capability", err)
	}
}

func TestClaudeLegacySessionWithoutNativeContextKeepsDirectSkillCompatibility(t *testing.T) {
	t.Parallel()

	if err := (&session{}).requireProviderNativeSkills([]dto.SkillRef{{Name: "review"}}); err != nil {
		t.Fatalf("requireProviderNativeSkills() error = %v, want nil for schema-zero legacy session", err)
	}
}
