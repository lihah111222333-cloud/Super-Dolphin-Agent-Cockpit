package uistate

import (
	"context"
	"testing"
)

func TestSetPreferenceAppliesV2PreferenceRuntimeSideEffects(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	assertStallThresholdPreferenceSideEffects(t, svc)
	assertInjectedPromptPreferenceSideEffects(t, svc)
}

func assertStallThresholdPreferenceSideEffects(t *testing.T, svc *service) {
	t.Helper()
	if err := svc.SetPreference(context.Background(), preferenceStallThresholdSec, 480); err != nil {
		t.Fatalf("SetPreference(stallThresholdSec) error = %v", err)
	}
	if got := svc.state.StallThresholdSec; got != 480 {
		t.Fatalf("state.StallThresholdSec = %d, want 480", got)
	}

	if err := svc.SetPreference(context.Background(), preferenceStallThresholdSec, 29); err == nil {
		t.Fatal("SetPreference(stallThresholdSec=29) error = nil, want validation failure")
	}
	if got := svc.state.StallThresholdSec; got != 480 {
		t.Fatalf("state.StallThresholdSec after invalid write = %d, want 480", got)
	}
	prefs, err := svc.GetPreferences(context.Background())
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs.StallThresholdSec != 480 {
		t.Fatalf("prefs.StallThresholdSec after invalid write = %d, want 480", prefs.StallThresholdSec)
	}
}

func assertInjectedPromptPreferenceSideEffects(t *testing.T, svc *service) {
	t.Helper()
	if err := svc.SetPreference(context.Background(), preferenceShowInjectedPromptInChat, true); err != nil {
		t.Fatalf("SetPreference(settings.showInjectedPromptInChat=true) error = %v", err)
	}
	if svc.state.ShowInjectedPromptInChat == nil || !*svc.state.ShowInjectedPromptInChat {
		t.Fatalf("state.ShowInjectedPromptInChat = %#v, want true", svc.state.ShowInjectedPromptInChat)
	}

	if err := svc.SetPreference(context.Background(), preferenceShowInjectedPromptInChat, false); err != nil {
		t.Fatalf("SetPreference(settings.showInjectedPromptInChat=false) error = %v", err)
	}
	if svc.state.ShowInjectedPromptInChat == nil || *svc.state.ShowInjectedPromptInChat {
		t.Fatalf("state.ShowInjectedPromptInChat = %#v, want false", svc.state.ShowInjectedPromptInChat)
	}
}

func TestGetPreferencesStructuresV2RuntimePreferenceKeys(t *testing.T) {
	t.Parallel()

	const projectCWD = "/tmp/preferences-runtime-contract"

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	ctx := withPreferenceScope(context.Background(), projectCWD)
	if err := svc.SetPreference(ctx, preferenceStallThresholdSec, 480); err != nil {
		t.Fatalf("SetPreference(stallThresholdSec) error = %v", err)
	}
	if err := svc.SetPreference(ctx, preferenceShowInjectedPromptInChat, false); err != nil {
		t.Fatalf("SetPreference(settings.showInjectedPromptInChat) error = %v", err)
	}

	prefs, err := svc.GetPreferences(ctx)
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs.CWD != projectCWD {
		t.Fatalf("prefs.CWD = %q, want %q", prefs.CWD, projectCWD)
	}
	if prefs.StallThresholdSec != 480 {
		t.Fatalf("prefs.StallThresholdSec = %d, want 480", prefs.StallThresholdSec)
	}
	if prefs.ShowInjectedPromptInChat == nil || *prefs.ShowInjectedPromptInChat {
		t.Fatalf("prefs.ShowInjectedPromptInChat = %#v, want false", prefs.ShowInjectedPromptInChat)
	}
}

func TestGetPreferencesDefaultsGlobalActiveProviderToCodex(t *testing.T) {
	t.Parallel()

	const providerPreferenceKey = "settings.provider.active"
	const providerPreferenceDefault = "codex"

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	prefs, err := svc.GetPreferences(context.Background())
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs.CWD != "" {
		t.Fatalf("prefs.CWD = %q, want global scope", prefs.CWD)
	}
	if got := prefs.Values[providerPreferenceKey]; got != providerPreferenceDefault {
		t.Fatalf("prefs.Values[%q] = %#v, want %q", providerPreferenceKey, got, providerPreferenceDefault)
	}
	if got := preferenceValue(*prefs, providerPreferenceKey); got != providerPreferenceDefault {
		t.Fatalf("preferenceValue(%q) = %#v, want %q", providerPreferenceKey, got, providerPreferenceDefault)
	}
}

func TestGetPreferencesDoesNotSynthesizeScopedProviderDefault(t *testing.T) {
	t.Parallel()

	const projectCWD = "/tmp/preferences-provider-default"
	const providerPreferenceKey = "settings.provider.active"

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	prefs, err := svc.GetPreferences(withPreferenceScope(context.Background(), projectCWD))
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if prefs.CWD != projectCWD {
		t.Fatalf("prefs.CWD = %q, want %q", prefs.CWD, projectCWD)
	}
	if got := prefs.Values[providerPreferenceKey]; got != "codex" {
		t.Fatalf("prefs.Values[%q] = %#v, want \"codex\" default for first-time packaged install", providerPreferenceKey, got)
	}
	if got := preferenceValue(*prefs, providerPreferenceKey); got != "codex" {
		t.Fatalf("preferenceValue(%q) = %#v, want \"codex\" default for first-time packaged install", providerPreferenceKey, got)
	}
}

func TestGetPreferencesScopedProviderInheritsGlobalPreference(t *testing.T) {
	t.Parallel()

	const projectCWD = "/tmp/preferences-provider-global"
	const providerPreferenceKey = "settings.provider.active"

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := svc.SetPreference(context.Background(), providerPreferenceKey, "claude"); err != nil {
		t.Fatalf("SetPreference(%s) error = %v", providerPreferenceKey, err)
	}

	prefs, err := svc.GetPreferences(withPreferenceScope(context.Background(), projectCWD))
	if err != nil {
		t.Fatalf("GetPreferences() error = %v", err)
	}
	if got := preferenceValue(*prefs, providerPreferenceKey); got != "claude" {
		t.Fatalf("preferenceValue(%q) = %#v, want global fallback claude", providerPreferenceKey, got)
	}
}

func TestGetStateProjectsInjectedPromptVisibilityPreference(t *testing.T) {
	t.Parallel()

	const projectCWD = "/tmp/preferences-state-contract"

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	ctx := withPreferenceScope(context.Background(), projectCWD)
	if err := svc.SetPreference(ctx, preferenceShowInjectedPromptInChat, true); err != nil {
		t.Fatalf("SetPreference(settings.showInjectedPromptInChat) error = %v", err)
	}

	state, err := svc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if state.ShowInjectedPromptInChat == nil || !*state.ShowInjectedPromptInChat {
		t.Fatalf("state.ShowInjectedPromptInChat = %#v, want true", state.ShowInjectedPromptInChat)
	}
}
