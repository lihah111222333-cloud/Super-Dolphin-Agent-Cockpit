package uistate

import (
	"context"
	"testing"
)

func TestSetPreferenceAppliesV2PreferenceRuntimeSideEffects(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

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

	svc, _, err := NewService(nil, nil, nil, nil)
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

func TestGetStateProjectsInjectedPromptVisibilityPreference(t *testing.T) {
	t.Parallel()

	const projectCWD = "/tmp/preferences-state-contract"

	svc, _, err := NewService(nil, nil, nil, nil)
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
