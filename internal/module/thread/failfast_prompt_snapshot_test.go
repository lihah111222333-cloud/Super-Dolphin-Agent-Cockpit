package thread

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestStoredPromptSnapshotRequiresExactVersion(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:      "snapshot",
		BaseInstructions: "base",
	}, "codex")
	assembly.Snapshot.Version--
	if storedPromptSnapshotValid(assembly.Snapshot, "codex") {
		t.Fatalf("storedPromptSnapshotValid() accepted version %d, want exact current version %d", assembly.Snapshot.Version, contract.PromptAssemblySnapshotVersion)
	}
}

func TestStablePromptSnapshotMigrationGateRejectsDisplayOnlySnapshot(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{thread: &ThreadRecord{
		ThreadID:       "thread-display-only-migration",
		AgentID:        "agent-display-only-migration",
		Prompt:         "legacy display",
		Cwd:            "/repo",
		ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
	}}
	svc := &service{threadStore: threads, logger: silentLogger()}
	_, err := svc.resolveStablePromptSnapshot(context.Background(), "thread-display-only-migration", "codex", contract.PromptAssemblySnapshot{})
	if err == nil || !strings.Contains(err.Error(), "prompt snapshot") {
		t.Fatalf("resolveStablePromptSnapshot() error = %v, want display-only snapshot rejection", err)
	}
}

func TestLegacyThreadUsesExplicitSnapshotMigrationGate(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, config []byte, wantErr bool) {
		t.Helper()
		threads := &stubThreadStore{thread: &ThreadRecord{
			ThreadID:         "thread-legacy",
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Prompt:           "legacy name",
			Model:            "gpt-5.5",
			Cwd:              "/repo",
			ConfigOverride:   config,
			CreatedAt:        123,
			Status:           statusCreated,
		}}
		const providerThreadID = "11111111-2222-3333-4444-555555555582"
		bindings := &stubBindingStore{binding: &BindingRecord{
			AgentID:          "agent-legacy",
			ParentAgentID:    "agent-root",
			AgentType:        "worker",
			AgentMemoryScope: "local",
			Provider:         "codex",
			ProviderThreadID: providerThreadID,
			CodexThreadID:    "thread-legacy",
			RolloutPath:      writeExistingProviderHistoryFile(t, providerThreadID),
			Cwd:              "/repo",
		}}
		sessions := &stubSessionProvider{}
		resumeCalled := false
		starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
			resumeCalled = true
			sessions.session = &stubSession{threadID: providerThreadID}
			return sessions.session, nil
		}}
		svc := NewServiceWithPromptAssembly(
			silentLogger(),
			threads,
			bindings,
			sessions,
			starter,
			nil,
			&forkOrchestrationStub{},
			nil,
			&resumeMetadataPromptAssembly{},
			nil,
			nil,
		).(*service)

		_, err := svc.Resume(context.Background(), ResumeRequest{ThreadID: "thread-legacy"})
		if wantErr {
			if err == nil || !strings.Contains(err.Error(), "legacy") {
				t.Fatalf("Resume() error = %v, want legacy migration gate error", err)
			}
			if resumeCalled {
				t.Fatal("ResumeSession was called without explicit legacy migration gate")
			}
			return
		}
		if err != nil {
			t.Fatalf("Resume() error = %v, want nil with explicit legacy migration gate", err)
		}
		if !resumeCalled {
			t.Fatal("ResumeSession was not called with explicit legacy migration gate")
		}
	}

	t.Run("without_gate_rejects", func(t *testing.T) {
		run(t, nil, true)
	})
	t.Run("with_gate_allows_migration", func(t *testing.T) {
		config := legacyPromptSnapshotMigrationConfig(t)
		run(t, config, false)
	})
}

func TestStartDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("snapshot write failed")
	threads := &stubThreadStore{savePromptSnapshotError: cause}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f607"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "start",
		PromptAssemblyRef: promptAssemblyForTest("start"),
	})
	if !errors.Is(err, cause) {
		t.Fatalf("Start() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before prompt snapshot is saved", startedCount)
	}
}

func TestForkDoesNotPublishStartedBeforeSnapshotSaved(t *testing.T) {
	t.Parallel()

	cause := errors.New("fork snapshot write failed")
	originalSession := &stubSession{
		threadID:   "thread-parent",
		forkResult: dto.ForkResult{NewThreadID: "thread-fork"},
	}
	forkedSession := &stubSession{threadID: "019d5f6b-aaaa-7760-9d6f-54005553f5b3"}
	sessions := &stubSessionProvider{session: originalSession}
	bindings := forkParentBindingStore()
	threads := forkParentThreadStore()
	threads.savePromptSnapshotError = cause
	starter := &stubSessionStarter{onResume: func(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
		sessions.session = forkedSession
		return forkedSession, nil
	}}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, &forkOrchestrationStub{}, nil).(*service)
	startedCount := 0
	svc.emitStarted = func(threaddto.Started) {
		startedCount++
	}

	_, err := svc.Fork(context.Background(), "thread-parent")
	if !errors.Is(err, cause) {
		t.Fatalf("Fork() error = %v, want %v", err, cause)
	}
	if startedCount != 0 {
		t.Fatalf("thread.Started emitted %d times; want 0 before fork prompt snapshot is saved", startedCount)
	}
}

func TestPromptSnapshotHashCoversSectionSnapshot(t *testing.T) {
	t.Parallel()

	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           "snapshot name",
		BaseInstructions:      "snapshot base",
		DeveloperInstructions: "snapshot dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "You are Codex."},
			{Name: "language", Content: "Use Chinese."},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 9},
	}, "codex")
	snapshot := assembly.Snapshot
	if !storedPromptSnapshotValid(snapshot, "codex") {
		t.Fatalf("storedPromptSnapshotValid() = false for freshly generated snapshot: %#v", snapshot)
	}

	tampered := snapshot
	tampered.SectionSnapshot = clonePromptSectionMap(snapshot.SectionSnapshot)
	tampered.SectionSnapshot["language"] = "Use French."
	if storedPromptSnapshotValid(tampered, "codex") {
		t.Fatal("storedPromptSnapshotValid() = true after section snapshot tamper, want false")
	}
}

func validThreadPromptSnapshotForTest(displayName string) PromptSnapshotRecord {
	assembly := ensureStartAssemblySnapshot(contract.StartAssembly{
		DisplayName:           displayName,
		BaseInstructions:      "stored base",
		DeveloperInstructions: "stored dev",
		ResolvedSections: []contract.ResolvedPromptSection{
			{Name: "identity", Content: "stable identity"},
		},
		Snapshot: contract.PromptAssemblySnapshot{Generation: 7},
	}, "codex")
	return promptSnapshotRecordToStore(toStoredPromptSnapshot(assembly.Snapshot))
}

// validPromptSnapshotRecordForTest 返回指定 provider 可直接复用的 exact-version snapshot。
// Resume/Recover 测试若只关注身份、配置或错误收敛，应显式携带该快照，避免绕过生产
// snapshot contract；需要覆盖缺失/旧版本的测试则继续使用空值或手工构造负例。
func validPromptSnapshotRecordForTest(provider, displayName string) PromptSnapshotRecord {
	record := validThreadPromptSnapshotForTest(displayName)
	record.Provider = strings.TrimSpace(provider)
	var boundary *dto.PromptAssemblyBoundary
	if record.Boundary != nil {
		boundary = &dto.PromptAssemblyBoundary{
			CachedPrefix: record.Boundary.CachedPrefix,
			UncachedTail: record.Boundary.UncachedTail,
		}
	}
	record.Hash = promptSnapshotHash(
		record.DisplayName,
		record.BaseInstructions,
		record.DeveloperInstructions,
		record.Provider,
		boundary,
		record.SectionSnapshot,
		record.Generation,
	)
	return record
}

func validPromptSnapshotPtrForTest(provider, displayName string) *PromptSnapshotRecord {
	record := validPromptSnapshotRecordForTest(provider, displayName)
	return &record
}

func legacyPromptSnapshotMigrationConfig(t *testing.T) []byte {
	t.Helper()
	config, err := encodeStoredThreadConfig(storedThreadConfig{
		Runtime: map[string]any{
			"legacyPromptSnapshotMigration": true,
		},
	})
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig() error = %v", err)
	}
	return config
}
