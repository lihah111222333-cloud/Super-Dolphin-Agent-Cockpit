package unified

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	goldentest "github.com/lihah111222333-cloud/super-dolphin-agent/internal/testutil/golden"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"
)

var sessionResolverGoldenOwner = goldentest.NewTestOwner(flag.Bool("update", false, "refresh golden JSON fixtures"))

type resumeCaptureDriver struct {
	name      string
	session   contract.Session
	resumeReq dto.ResumeSessionRequest
	resumed   int
}

func (d *resumeCaptureDriver) Name() string { return d.name }
func (d *resumeCaptureDriver) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return d.session, nil
}
func (d *resumeCaptureDriver) ResumeSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	d.resumed++
	d.resumeReq = req
	return d.session, nil
}

type resolvingResumeCaptureDriver struct {
	*resumeCaptureDriver
	resolveReq         dto.ResumeSessionRequest
	codexHome          string
	codexInstanceKey   string
	codexModelProvider string
}

func (d *resolvingResumeCaptureDriver) ResolveResumeSessionIdentity(_ context.Context, req dto.ResumeSessionRequest) (dto.ResumeSessionRequest, error) {
	d.resolveReq = req
	if req.Config == nil {
		req.Config = map[string]any{}
	}
	req.CodexHome = firstNonEmptyTestString(d.codexHome, "/Users/test/.codex")
	req.CodexInstanceKey = firstNonEmptyTestString(d.codexInstanceKey, "default")
	req.CodexModelProvider = firstNonEmptyTestString(d.codexModelProvider, "openai")
	req.Config["codexHome"] = req.CodexHome
	req.Config["codexInstanceKey"] = req.CodexInstanceKey
	req.Config["codexModelProvider"] = req.CodexModelProvider
	return req, nil
}

type recordingSessionBindingUpserter struct {
	calls   int
	binding contract.SessionBinding
	err     error
}

func (w *recordingSessionBindingUpserter) UpsertSessionBinding(_ context.Context, binding contract.SessionBinding) error {
	w.calls++
	w.binding = binding
	return w.err
}

func TestSessionResolverAutoResumePassesCodexIdentityGolden(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, err := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111111")
	if err != nil {
		t.Fatalf("write provider history fixture: %v", err)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111111"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-1",
			AgentID:        "agent-1",
			PromptSnapshot: autoResumePromptSnapshotForTest(),
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "11111111-aaaa-bbbb-cccc-111111111111",
				RolloutPath:        rolloutPath,
				Cwd:                "/repo",
				CodexHome:          "/Users/test/.codex",
				CodexInstanceKey:   "codex-instance-key-1",
				CodexModelProvider: "openai",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	goldentest.AssertJSON(t, sessionResolverGoldenOwner, goldentest.Case{
		BaseDir: "testdata/golden",
		Domain:  goldentest.DomainIntegration,
		Name:    "auto_resume_identity_request",
	}, driver.resumeReq)
}

func TestSessionResolverAutoResumePassesRuntimeConfig(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111112")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111112"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-1",
			AgentID:        "agent-1",
			PromptSnapshot: autoResumePromptSnapshotForTest(),
			RuntimeConfig: map[string]any{
				"additionalWorkingDirectories": []any{"/repo/extra"},
			},
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111112",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	want := map[string]any{"additionalWorkingDirectories": []any{"/repo/extra"}}
	if !reflect.DeepEqual(driver.resumeReq.Config, want) {
		t.Fatalf("ResumeSession Config = %#v, want %#v", driver.resumeReq.Config, want)
	}
}

func TestSessionResolverAutoResumePassesAuthoritativeClaudeHome(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-aaaa-bbbb-cccc-111111111119"
	claudeHome := t.TempDir()
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID, "claude", claudeHome)
	driver := &resumeCaptureDriver{
		name:    "claude",
		session: &generationTestSession{threadID: providerThreadID},
	}
	snapshot := autoResumePromptSnapshotForTest()
	snapshot.Provider = "claude"
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-claude-owner",
			AgentID:        "agent-claude-owner",
			PromptSnapshot: snapshot,
			RuntimeConfig: map[string]any{
				"claudeHome": t.TempDir(),
			},
		}},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"claude:" + providerThreadID: {
				Provider:             "claude",
				AgentID:              "agent-claude-owner",
				ProviderThreadID:     providerThreadID,
				RolloutPath:          rolloutPath,
				Cwd:                  "/repo",
				ProviderRecoveryHome: claudeHome,
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "claude", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-claude-owner"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.ClaudeHome != claudeHome {
		t.Fatalf("ResumeSessionRequest.ClaudeHome = %q, want authoritative binding owner %q", driver.resumeReq.ClaudeHome, claudeHome)
	}
}

func TestBuildAutoResumeRequestDoesNotSetClaudeHomeForCodex(t *testing.T) {
	t.Parallel()

	request := buildAutoResumeRequest(
		&contract.SessionBinding{
			AgentID:              "agent-codex-owner",
			ProviderRecoveryHome: "/instances/codex-a",
		},
		map[string]any{"claudeHome": "/runtime/claude-override"},
		autoResumePromptSnapshotForTest(),
		"codex",
		"11111111-aaaa-bbbb-cccc-111111111120",
		"/repo",
		nil,
	)
	if request.ClaudeHome != "" {
		t.Fatalf("Codex ResumeSessionRequest.ClaudeHome = %q, want empty", request.ClaudeHome)
	}
}

func TestBuildAutoResumePlanRejectsEmptyClaudeOwner(t *testing.T) {
	t.Parallel()

	const providerThreadID = "11111111-aaaa-bbbb-cccc-111111111121"
	claudeHome := t.TempDir()
	rolloutPath := writeExistingProviderHistoryFile(t, providerThreadID, "claude", claudeHome)
	driver := &resumeCaptureDriver{name: "claude", session: &generationTestSession{threadID: providerThreadID}}
	resolver := &sessionResolver{
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "claude", Create: func() contract.Driver { return driver }},
		}}),
	}
	snapshot := autoResumePromptSnapshotForTest()
	snapshot.Provider = "claude"
	_, err := resolver.buildAutoResumePlan(&contract.SessionBinding{
		Provider:         "claude",
		AgentID:          "agent-claude-empty-owner",
		ProviderThreadID: providerThreadID,
		RolloutPath:      rolloutPath,
		Cwd:              "/repo",
	}, map[string]any{"claudeHome": claudeHome}, snapshot)
	var recoveryErr *providerrecovery.Error
	if !errors.As(err, &recoveryErr) || recoveryErr.Kind != providerrecovery.ErrorKindInvalidIdentity {
		t.Fatalf("buildAutoResumePlan() error = %v, want provider recovery invalid_identity", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want none before owner validation", driver.resumed)
	}
}

func TestSessionResolverAutoResumePassesPromptSnapshot(t *testing.T) {
	t.Parallel()

	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111116")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111116"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			PromptSnapshot: contract.PromptAssemblySnapshot{
				BaseInstructions: "resume system prompt",
				Provider:         "codex",
				Version:          2,
				Hash:             "snapshot-hash",
			},
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111116",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.PromptSnapshot.BaseInstructions != "resume system prompt" ||
		driver.resumeReq.PromptSnapshot.Version != 2 ||
		driver.resumeReq.PromptSnapshot.Hash != "snapshot-hash" {
		t.Fatalf("ResumeSession PromptSnapshot = %#v, want stored snapshot", driver.resumeReq.PromptSnapshot)
	}
}

func TestSessionResolverAutoResumeRejectsMissingPromptSnapshot(t *testing.T) {
	t.Parallel()

	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("11111111-aaaa-bbbb-cccc-111111111117")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "11111111-aaaa-bbbb-cccc-111111111117"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID: "public-thread-1",
			AgentID:  "agent-1",
			Status:   "running",
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:         "codex",
				AgentID:          "agent-1",
				ProviderThreadID: "11111111-aaaa-bbbb-cccc-111111111117",
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
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("ResolveSession() error = %v, want prompt snapshot error", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0 without prompt snapshot", driver.resumed)
	}
}

func TestSessionResolverAutoResumeBackfillsCodexIdentityFromRuntimeConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		runtime map[string]any
	}{
		{
			name: "canonical keys",
			runtime: map[string]any{
				"codexHome":          "/runtime/.codex",
				"codexInstanceKey":   "runtime-instance-key",
				"codexModelProvider": "runtime-provider",
			},
		},
		{
			name: "snake case aliases",
			runtime: map[string]any{
				"codex_home":           "/runtime/snake/.codex",
				"codex_instance_key":   "runtime-snake-instance-key",
				"codex_model_provider": "runtime-snake-provider",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newUnifiedRecoveryTestFixture(t)
			rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("66666666-aaaa-bbbb-cccc-666666666666")
			if fixtureErr != nil {
				t.Fatalf("write provider history fixture: %v", fixtureErr)
			}
			driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "66666666-aaaa-bbbb-cccc-666666666666"}}
			resolver := &sessionResolver{
				threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
					ThreadID:       "public-thread-1",
					AgentID:        "agent-1",
					PromptSnapshot: autoResumePromptSnapshotForTest(),
					RuntimeConfig:  tc.runtime,
				}},
				bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
					"codex:provider-thread-1": {
						Provider:         "codex",
						AgentID:          "agent-1",
						ProviderThreadID: "66666666-aaaa-bbbb-cccc-666666666666",
						RolloutPath:      rolloutPath,
						Cwd:              "/repo",
					},
				}}),
				registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
					{Name: "codex", Create: func() contract.Driver { return driver }},
				}}),
				sessions: NewSessionManager(nil),
			}

			if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
				t.Fatalf("ResolveSession() error = %v", err)
			}
			if driver.resumeReq.CodexHome != codexIdentityTestString(tc.runtime, "codexHome", "codex_home") ||
				driver.resumeReq.CodexInstanceKey != codexIdentityTestString(tc.runtime, "codexInstanceKey", "codex_instance_key") ||
				driver.resumeReq.CodexModelProvider != codexIdentityTestString(tc.runtime, "codexModelProvider", "codex_model_provider") {
				t.Fatalf("ResumeSession codex identity = %q/%q/%q, want runtime config identity",
					driver.resumeReq.CodexHome,
					driver.resumeReq.CodexInstanceKey,
					driver.resumeReq.CodexModelProvider)
			}
		})
	}
}

func TestSessionResolverAutoResumeResolvesAndBackfillsLegacyCodexIdentity(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("99999999-aaaa-bbbb-cccc-999999999999")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	writer := &recordingSessionBindingUpserter{}
	driver := &resolvingResumeCaptureDriver{resumeCaptureDriver: &resumeCaptureDriver{
		name:    "codex",
		session: &generationTestSession{threadID: "99999999-aaaa-bbbb-cccc-999999999999"},
	}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-legacy",
			AgentID:        "agent-legacy",
			PromptSnapshot: autoResumePromptSnapshotForTest(),
			RuntimeConfig: map[string]any{
				"provider": "codex",
				"cwd":      "/repo",
			},
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-legacy": {
				Provider:         "codex",
				AgentID:          "agent-legacy",
				ProviderThreadID: "99999999-aaaa-bbbb-cccc-999999999999",
				CodexThreadID:    "public-thread-legacy",
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
				CreatedAt:        123,
			},
		}}),
		bindingWriter: writer,
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-legacy"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.CodexHome != "/Users/test/.codex" ||
		driver.resumeReq.CodexInstanceKey != "default" ||
		driver.resumeReq.CodexModelProvider != "openai" {
		t.Fatalf("ResumeSession codex identity = %q/%q/%q, want resolved default identity",
			driver.resumeReq.CodexHome,
			driver.resumeReq.CodexInstanceKey,
			driver.resumeReq.CodexModelProvider)
	}
	if writer.calls != 1 {
		t.Fatalf("binding backfill calls = %d, want 1", writer.calls)
	}
	if writer.binding.CodexHome != "/Users/test/.codex" ||
		writer.binding.CodexInstanceKey != "default" ||
		writer.binding.CodexModelProvider != "openai" {
		t.Fatalf("backfilled codex identity = %q/%q/%q, want resolved default identity",
			writer.binding.CodexHome,
			writer.binding.CodexInstanceKey,
			writer.binding.CodexModelProvider)
	}
}

func TestAutoResumeBackfillWritesCanonicalCodexIdentity(t *testing.T) {
	canonicalHome, aliasHome := createAutoResumeCodexHomeCleanAlias(t)
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", "codex", canonicalHome)
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	writer := &recordingSessionBindingUpserter{}
	driver := &resolvingResumeCaptureDriver{
		resumeCaptureDriver: &resumeCaptureDriver{
			name:    "codex",
			session: &generationTestSession{threadID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"},
		},
		codexHome:          canonicalHome,
		codexInstanceKey:   "default",
		codexModelProvider: "openai",
	}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-canonical-backfill",
			AgentID:        "agent-canonical-backfill",
			PromptSnapshot: autoResumePromptSnapshotForTest(),
			RuntimeConfig: map[string]any{
				contract.CodexHomeKey:          aliasHome,
				contract.CodexInstanceKeyKey:   "default",
				contract.CodexModelProviderKey: "openai",
			},
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-canonical-backfill": {
				Provider:           "codex",
				AgentID:            "agent-canonical-backfill",
				ProviderThreadID:   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				CodexThreadID:      "public-thread-canonical-backfill",
				RolloutPath:        rolloutPath,
				Cwd:                t.TempDir(),
				CodexHome:          aliasHome,
				CodexInstanceKey:   "default",
				CodexModelProvider: "openai",
				CreatedAt:          123,
			},
		}}),
		bindingWriter: writer,
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-canonical-backfill"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resolveReq.CodexHome != aliasHome {
		t.Fatalf("resolver input codex home = %q, want alias %q", driver.resolveReq.CodexHome, aliasHome)
	}
	if driver.resumeReq.CodexHome != canonicalHome {
		t.Fatalf("ResumeSession codex home = %q, want canonical %q", driver.resumeReq.CodexHome, canonicalHome)
	}
	if writer.calls != 1 {
		t.Fatalf("binding backfill calls = %d, want 1", writer.calls)
	}
	if writer.binding.CodexHome != canonicalHome ||
		writer.binding.CodexInstanceKey != "default" ||
		writer.binding.CodexModelProvider != "openai" {
		t.Fatalf("backfilled codex identity = %q/%q/%q, want canonical %q/default/openai",
			writer.binding.CodexHome,
			writer.binding.CodexInstanceKey,
			writer.binding.CodexModelProvider,
			canonicalHome)
	}
}

func TestSessionResolverAutoResumePrefersBindingCodexIdentityOverRuntimeConfig(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("77777777-aaaa-bbbb-cccc-777777777777")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "77777777-aaaa-bbbb-cccc-777777777777"}}
	resolver := &sessionResolver{
		threadStore: stubThreadLookup{thread: &contract.SessionThreadRef{
			ThreadID:       "public-thread-1",
			AgentID:        "agent-1",
			PromptSnapshot: autoResumePromptSnapshotForTest(),
			RuntimeConfig: map[string]any{
				"codexHome":          "/runtime/.codex",
				"codexInstanceKey":   "runtime-instance-key",
				"codexModelProvider": "runtime-provider",
			},
		}},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:provider-thread-1": {
				Provider:           "codex",
				AgentID:            "agent-1",
				ProviderThreadID:   "77777777-aaaa-bbbb-cccc-777777777777",
				RolloutPath:        rolloutPath,
				Cwd:                "/repo",
				CodexHome:          "/binding/.codex",
				CodexInstanceKey:   "binding-instance-key",
				CodexModelProvider: "binding-provider",
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "public-thread-1"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.CodexHome != "/binding/.codex" ||
		driver.resumeReq.CodexInstanceKey != "binding-instance-key" ||
		driver.resumeReq.CodexModelProvider != "binding-provider" {
		t.Fatalf("ResumeSession codex identity = %q/%q/%q, want binding identity",
			driver.resumeReq.CodexHome,
			driver.resumeReq.CodexInstanceKey,
			driver.resumeReq.CodexModelProvider)
	}
}

func codexIdentityTestString(config map[string]any, keys ...string) string {
	for _, key := range keys {
		value, _ := config[key].(string)
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyTestString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func TestSessionResolverProviderThreadAutoResumeDoesNotUseCodexThreadID(t *testing.T) {
	// Phase 2 of the session-stopped rootfix removed the
	// binding.CodexThreadID -> req.ThreadID fallback because CodexThreadID is
	// a routing key (often agent-placeholder value) and feeding it into the
	// driver as a thread id let placeholders cross provider boundaries into
	// claudecli, which caused the 5s system:init deadlock. After the change
	// req.ThreadID stays empty when no public thread id is provided, even if
	// CodexThreadID happens to hold a non-empty value.
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("33333333-aaaa-bbbb-cccc-333333333333")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "33333333-aaaa-bbbb-cccc-333333333333"}}
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"public-thread-3": {
				ThreadID:       "public-thread-3",
				AgentID:        "agent-3",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:33333333-aaaa-bbbb-cccc-333333333333": {
				Provider:         "codex",
				AgentID:          "agent-3",
				ProviderThreadID: "33333333-aaaa-bbbb-cccc-333333333333",
				CodexThreadID:    "public-thread-3",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "33333333-aaaa-bbbb-cccc-333333333333"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	if driver.resumeReq.ThreadID != "" {
		t.Fatalf("ThreadID = %q, want empty (CodexThreadID fallback removed)", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.AgentID != "agent-3" {
		t.Fatalf("AgentID = %q, want agent-3", driver.resumeReq.AgentID)
	}
	if driver.resumeReq.ProviderThreadID != "33333333-aaaa-bbbb-cccc-333333333333" {
		t.Fatalf("ProviderThreadID = %q, want 33333333-aaaa-bbbb-cccc-333333333333", driver.resumeReq.ProviderThreadID)
	}
}

func TestSessionResolverAutoResumeDoesNotUseAgentIDAsThreadIDWithoutPublicThread(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("22222222-aaaa-bbbb-cccc-222222222222")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "22222222-aaaa-bbbb-cccc-222222222222"}}
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"agent-2": {
				ThreadID:       "agent-2",
				AgentID:        "agent-2",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:22222222-aaaa-bbbb-cccc-222222222222": {
				Provider:         "codex",
				AgentID:          "agent-2",
				ProviderThreadID: "22222222-aaaa-bbbb-cccc-222222222222",
				RolloutPath:      rolloutPath,
				Cwd:              t.TempDir(),
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "22222222-aaaa-bbbb-cccc-222222222222"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumeReq.ThreadID == "agent-2" {
		t.Fatalf("auto-resume ThreadID = AgentID %q; want public thread id or empty when unavailable", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ThreadID != "" {
		t.Fatalf("auto-resume ThreadID = %q, want empty without a public thread id source", driver.resumeReq.ThreadID)
	}
	if driver.resumeReq.ProviderThreadID != "22222222-aaaa-bbbb-cccc-222222222222" {
		t.Fatalf("ProviderThreadID = %q, want 22222222-aaaa-bbbb-cccc-222222222222", driver.resumeReq.ProviderThreadID)
	}
}

func TestSessionResolverAutoResumeAcceptsOfficialCodexUUIDWithoutHistoryFile(t *testing.T) {
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "44444444-aaaa-bbbb-cccc-444444444444"}}
	cwd := t.TempDir()
	resolver := &sessionResolver{
		threadStore: keyedThreadLookup{
			"agent-4": {
				ThreadID:       "agent-4",
				AgentID:        "agent-4",
				Status:         "running",
				PromptSnapshot: autoResumePromptSnapshotForTest(),
			},
		},
		bindingStore: stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:44444444-aaaa-bbbb-cccc-444444444444": {
				Provider:         "codex",
				AgentID:          "agent-4",
				ProviderThreadID: "44444444-aaaa-bbbb-cccc-444444444444",
				CodexHome:        cwd,
				Cwd:              cwd,
			},
		}},
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	if _, err := resolver.ResolveSession(context.Background(), "44444444-aaaa-bbbb-cccc-444444444444"); err != nil {
		t.Fatalf("ResolveSession() error = %v", err)
	}
	if driver.resumed != 1 {
		t.Fatalf("ResumeSession calls = %d, want 1", driver.resumed)
	}
	if driver.resumeReq.ProviderThreadID != "44444444-aaaa-bbbb-cccc-444444444444" {
		t.Fatalf("ProviderThreadID = %q, want official Codex UUID", driver.resumeReq.ProviderThreadID)
	}
}

func TestSessionResolverAutoResumeRejectsMissingBindingCWD(t *testing.T) {
	fixture := newUnifiedRecoveryTestFixture(t)
	rolloutPath, fixtureErr := fixture.writeExistingProviderHistoryFile("55555555-aaaa-bbbb-cccc-555555555555")
	if fixtureErr != nil {
		t.Fatalf("write provider history fixture: %v", fixtureErr)
	}
	driver := &resumeCaptureDriver{name: "codex", session: &generationTestSession{threadID: "55555555-aaaa-bbbb-cccc-555555555555"}}
	resolver := &sessionResolver{
		bindingStore: fixture.bindingLookup(stubBindingLookup{bindings: map[string]*contract.SessionBinding{
			"codex:55555555-aaaa-bbbb-cccc-555555555555": {
				Provider:         "codex",
				AgentID:          "agent-5",
				ProviderThreadID: "55555555-aaaa-bbbb-cccc-555555555555",
				RolloutPath:      rolloutPath,
			},
		}}),
		registry: NewRegistry(RegistryParams{Drivers: []contract.DriverFactory{
			{Name: "codex", Create: func() contract.Driver { return driver }},
		}}),
		sessions: NewSessionManager(nil),
	}

	_, err := resolver.ResolveSession(context.Background(), "55555555-aaaa-bbbb-cccc-555555555555")
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("ResolveSession() error = %v, want cwd required", err)
	}
	if driver.resumed != 0 {
		t.Fatalf("ResumeSession calls = %d, want 0", driver.resumed)
	}
}

func writeExistingProviderHistoryFile(t *testing.T, args ...string) string {
	path, _ := writeProviderHistoryFile(t, args...)
	return path
}

func (f *unifiedRecoveryTestFixture) writeExistingProviderHistoryFile(args ...string) (string, error) {
	if f == nil || f.t == nil || f.recoveryHomeByPath == nil {
		return "", errors.New("unified recovery test fixture owner is required")
	}
	path, home := writeProviderHistoryFile(f.t, args...)
	if err := f.registerRecoveryHome(path, home); err != nil {
		return "", err
	}
	return path, nil
}

func writeProviderHistoryFile(t *testing.T, args ...string) (string, string) {
	t.Helper()
	identity := "00000000-0000-4000-8000-000000000001"
	provider := "codex"
	if len(args) > 0 {
		identity = args[0]
	}
	if len(args) > 1 {
		provider = args[1]
	}
	home := t.TempDir()
	if len(args) > 2 {
		home = args[2]
	}
	root := filepath.Join(home, "sessions", "2026", "07", "29")
	name := "rollout-test-" + identity + ".jsonl"
	content := fmt.Sprintf("{\"type\":\"session_meta\",\"payload\":{\"id\":%q}}\n", identity)
	if provider == "claude" {
		root = filepath.Join(home, "projects", "test-project")
		name = identity + ".jsonl"
		content = fmt.Sprintf("{\"sessionId\":%q,\"type\":\"user\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"test\"}]}}\n", identity)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create provider history root: %v", err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write provider history file: %v", err)
	}
	return path, home
}

func createAutoResumeCodexHomeCleanAlias(t *testing.T) (string, string) {
	t.Helper()
	realHome := t.TempDir()
	aliasHome := realHome + string(os.PathSeparator) + "."
	canonicalHome, err := contract.CanonicalizeCodexHome(realHome)
	if err != nil {
		t.Fatalf("CanonicalizeCodexHome(real home) error = %v", err)
	}
	return canonicalHome, aliasHome
}
