package unified

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

func TestSessionResolverDoesNotAutoResumeStoppedOrArchivedThread(t *testing.T) {
	t.Parallel()

	for _, status := range []string{"stopped", "archived"} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			fixture := newUnifiedRecoveryTestFixture(t)
			rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111113")
			if fixtureErr != nil {
				t.Fatalf("write provider history fixture: %v", fixtureErr)
			}
			driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111113"}}
			resolver := &sessionResolver{
				threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
					ThreadID: "public-thread-1",
					AgentID:  "agent-1",
					Status:   status,
				}},
				bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
					"codex:provider-thread-1": {
						Provider:         "codex",
						AgentID:          "agent-1",
						ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111113",
						RolloutPath:      rolloutPath,
						Cwd:              "/repo",
					},
				}}),
				registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
					{Name: "codex", Create: func() contract.Driver { return driver }},
				}}),
				sessions: NewSessionManager(nil),
			}

			_, err := resolver.ResolveSession(context.Background(), "public-thread-1")
			if err == nil || !strings.Contains(err.Error(), status) {
				t.Fatalf("ResolveSession() error = %v, want lifecycle %q error", err, status)
			}
			if driver.resumed != 0 {
				t.Fatalf("ResumeSession calls = %d, want 0 for %s thread", driver.resumed, status)
			}
		})
	}
}

func TestAutoResumeRejectsArchivedBinding(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		resolveID string
	}{
		{name: "public thread", resolveID: "public-thread-archived"},
		{name: "provider thread", resolveID: "11111111-aaaa-bbbb-cccc-111111111118"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fixture := newUnifiedRecoveryTestFixture(t)
			rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111118")
			if fixtureErr != nil {
				t.Fatalf("write provider history fixture: %v", fixtureErr)
			}
			driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111118"}}
			resolver := &sessionResolver{
				threadStore: keyedThreadLookup{
					"public-thread-archived": {
						ThreadID:       "public-thread-archived",
						AgentID:        "agent-archived",
						Status:         "running",
						PromptSnapshot: autoResumePromptSnapshotForTest(),
					},
				},
				bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
					"codex:11111111-aaaa-bbbb-cccc-111111111118": {
						Provider:         "codex",
						AgentID:          "agent-archived",
						ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111118",
						CodexThreadID:    "public-thread-archived",
						RolloutPath:      rolloutPath,
						Cwd:              "/repo",
						Archived:         true,
					},
				}}),
				registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
					{Name: "codex", Create: func() contract.Driver { return driver }},
				}}),
				sessions: NewSessionManager(nil),
			}

			_, err := resolver.ResolveSession(context.Background(), tc.resolveID)
			if err == nil || !strings.Contains(err.Error(), "archived") {
				t.Fatalf("ResolveSession() error = %v, want archived binding rejection", err)
			}
			if driver.resumed != 0 {
				t.Fatalf("ResumeSession calls = %d, want 0 for archived binding", driver.resumed)
			}
		})
	}
}

func TestSessionResolverProviderThreadDoesNotAutoResumeStoppedThread(t *testing.T) {
	t.Parallel()

	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111114")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111114"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			Status:   "stopped",
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:11111111-aaaa-bbbb-cccc-111111111114": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111114",
				CodexThreadID:    "public-thread-1",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "11111111-aaaa-bbbb-cccc-111111111114")
	if err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Fatalf("ResolveSession() error = %v, want stopped lifecycle error", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0 for stopped provider-thread route", driver.resumed)
	}
}

func TestAutoResumeRuntimeConfigFailsOnThreadStoreError(t *testing.T) {
	t.Parallel()

	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111115")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	wantErr := errors.New("thread config decode failed")
	threadStore := &sequenceThreadLookup{
		refs: []*contract.SessionThreadRef{
			nil,
			{ThreadID: "public-thread-1", AgentID: "agent-1", Status: "running"},
			nil,
			nil,
		},
		errs: []error{platformdb.ErrNotFound, nil, platformdb.ErrNotFound, wantErr},
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111115"}}
	resolver := &sessionResolver{
		threadStore: threadStore,
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:11111111-aaaa-bbbb-cccc-111111111115": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111115",
				CodexThreadID:    "public-thread-1",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "11111111-aaaa-bbbb-cccc-111111111115")
	if err == nil || !strings.Contains(err.Error(), "thread config decode failed") {
		t.Fatalf("ResolveSession() error = %v, want runtime config store error", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0 when runtime config lookup fails", driver.resumed)
	}
}
