package memory

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestKairos(t *testing.T) {
	t.Run("LoadMemoryPromptUsesDailyLogProtocol", TestKairosLoadMemoryPromptUsesDailyLogProtocol)
	t.Run("GetAutoMemDailyLogPath", TestKairosGetAutoMemDailyLogPath)
	t.Run("DailyLogWriterAppendsAndRollsOver", TestKairosDailyLogWriterAppendsAndRollsOver)
	t.Run("DurableTurnWritesWithoutExplicitRemember", TestKairosDurableTurnWritesWithoutExplicitRemember)
	t.Run("DailyLogSkipsChildAgent", TestKairosDailyLogSkipsChildAgent)
	t.Run("DateChangeAttachmentTail", TestKairosDateChangeAttachmentTail)
}

func TestKairosLoadMemoryPromptUsesDailyLogProtocol(t *testing.T) {
	text := goldenPromptString(LoadMemoryPrompt(MemoryModeKairos, true, false, true, []string{"Prefer absolute dates in daily logs."}))
	for _, snippet := range []string{
		"KAIROS daily log mode",
		"logs/YYYY/MM/YYYY-MM-DD.md",
		"- [HH:MM] content",
		"date_change",
		"Whenever durable remember-worthy information becomes clear",
		"read-only orientation summary",
		"Prefer absolute dates in daily logs.",
	} {
		if !strings.Contains(text, snippet) {
			t.Fatalf("Kairos prompt missing %q:\n%s", snippet, text)
		}
	}
	if strings.Contains(text, "### 1. memory system") {
		t.Fatalf("Kairos prompt unexpectedly reused Standard prompt heading:\n%s", text)
	}
}

func TestKairosGetAutoMemDailyLogPath(t *testing.T) {
	baseRoot := t.TempDir()
	projectRoot := newTestGitProjectRoot(t)
	initGitRepoForMemoryTest(t, projectRoot)
	when := time.Date(2026, 4, 15, 9, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	root, err := GetAutoMemPath(baseRoot, projectRoot)
	if err != nil {
		t.Fatalf("GetAutoMemPath() error = %v", err)
	}
	got, err := GetAutoMemDailyLogPath(baseRoot, projectRoot, when)
	if err != nil {
		t.Fatalf("GetAutoMemDailyLogPath() error = %v", err)
	}
	want := filepath.Join(root, "logs", "2026", "04", "2026-04-15.md")
	if got != want {
		t.Fatalf("GetAutoMemDailyLogPath() = %q, want %q", got, want)
	}
}

func initGitRepoForMemoryTest(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "init", root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", root, err, string(output))
	}
}

func TestKairosDailyLogWriterAppendsAndRollsOver(t *testing.T) {
	baseRoot := newTestMemoryRoot(t)
	projectRoot := newTestGitProjectRoot(t)
	cfg := &Config{
		Enabled:     true,
		RootDir:     baseRoot,
		ProjectRoot: projectRoot,
		Features:    MemoryFeatureFlags{Kairos: true},
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	thread := &contract.ThreadMetadata{
		ThreadID: "thread-1",
		ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{
			"threadKind":   "main",
			"sessionFlags": map[string]any{"memory_kairos": true},
		}),
	}
	hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, threadLookupStub{thread: thread}, nil, NewMemoryExtractor(), NewManifestBuilder())
	current := time.Date(2026, 4, 15, 9, 15, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	hooks.timeNow = func() time.Time { return current }

	dayOnePath := autoMemDailyLogPath(root, current)
	evt := turnCompletedEvent("thread-1", "turn-1")
	evt.Message = "I'll remember: use guarded build commands"
	hooks.onTurnCompleted(context.Background(), evt)

	current = time.Date(2026, 4, 15, 9, 31, 0, 0, current.Location())
	evt = turnCompletedEvent("thread-1", "turn-2")
	evt.Message = "I'll remember: prefer absolute dates in incident summaries"
	hooks.onTurnCompleted(context.Background(), evt)

	dayOneRaw, err := os.ReadFile(dayOnePath)
	if err != nil {
		t.Fatalf("ReadFile(day one) error = %v", err)
	}
	dayOneLines := strings.Split(strings.TrimSpace(string(dayOneRaw)), "\n")
	wantDayOne := []string{
		"- [09:15] use guarded build commands",
		"- [09:31] prefer absolute dates in incident summaries",
	}
	if strings.Join(dayOneLines, "\n") != strings.Join(wantDayOne, "\n") {
		t.Fatalf("day one log = %#v, want %#v", dayOneLines, wantDayOne)
	}

	current = time.Date(2026, 4, 16, 0, 5, 0, 0, current.Location())
	dayTwoPath := autoMemDailyLogPath(root, current)
	evt = turnCompletedEvent("thread-1", "turn-3")
	evt.Message = "I'll remember: start a fresh daily bucket after midnight"
	hooks.onTurnCompleted(context.Background(), evt)

	dayOneRaw, err = os.ReadFile(dayOnePath)
	if err != nil {
		t.Fatalf("ReadFile(day one after rollover) error = %v", err)
	}
	if strings.Count(strings.TrimSpace(string(dayOneRaw)), "\n") != 1 {
		t.Fatalf("day one log changed after rollover:\n%s", dayOneRaw)
	}
	dayTwoRaw, err := os.ReadFile(dayTwoPath)
	if err != nil {
		t.Fatalf("ReadFile(day two) error = %v", err)
	}
	if got := strings.TrimSpace(string(dayTwoRaw)); got != "- [00:05] start a fresh daily bucket after midnight" {
		t.Fatalf("day two log = %q", got)
	}
}

func TestKairosDurableTurnWritesWithoutExplicitRemember(t *testing.T) {
	baseRoot := newTestMemoryRoot(t)
	projectRoot := newTestGitProjectRoot(t)
	cfg := &Config{
		Enabled:     true,
		RootDir:     baseRoot,
		ProjectRoot: projectRoot,
		Features:    MemoryFeatureFlags{Kairos: true},
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	thread := &contract.ThreadMetadata{
		ThreadID: "thread-1",
		ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{
			"threadKind":   "main",
			"sessionFlags": map[string]any{"memory_kairos": true},
		}),
	}
	hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, threadLookupStub{thread: thread}, nil, NewMemoryExtractor(), NewManifestBuilder())
	current := time.Date(2026, 4, 15, 10, 45, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	hooks.timeNow = func() time.Time { return current }

	evt := turnCompletedEvent("thread-1", "turn-1")
	evt.Message = "This project prefers guarded build commands and absolute dates in incident summaries."
	hooks.onTurnCompleted(context.Background(), evt)

	raw, err := os.ReadFile(autoMemDailyLogPath(root, current))
	if err != nil {
		t.Fatalf("ReadFile(daily log) error = %v", err)
	}
	want := "- [10:45] This project prefers guarded build commands and absolute dates in incident summaries."
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Fatalf("daily log = %q, want %q", got, want)
	}
}

func TestKairosDailyLogSkipsChildAgent(t *testing.T) {
	baseRoot := newTestMemoryRoot(t)
	projectRoot := newTestGitProjectRoot(t)
	cfg := &Config{
		Enabled:     true,
		RootDir:     baseRoot,
		ProjectRoot: projectRoot,
		Features:    MemoryFeatureFlags{Kairos: true},
	}
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		t.Fatalf("resolvedStoreRoot() error = %v", err)
	}
	thread := &contract.ThreadMetadata{
		ThreadID:         "thread-1",
		ParentAgentID:    "agent-root",
		AgentMemoryScope: "project",
		ConfigOverride: mustStoredRuntimeConfig(t, map[string]any{
			"threadKind":   "child_agent",
			"sessionFlags": map[string]any{"memory_kairos": true},
		}),
	}
	hooks := newMemoryLifecycleHooks(cfg, nil, nil, nil, threadLookupStub{thread: thread}, nil, NewMemoryExtractor(), NewManifestBuilder())
	current := time.Date(2026, 4, 15, 11, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	hooks.timeNow = func() time.Time { return current }

	evt := turnCompletedEvent("thread-1", "turn-1")
	evt.Message = "I'll remember: worker-specific reminder"
	hooks.onTurnCompleted(context.Background(), evt)

	_, err = os.Stat(autoMemDailyLogPath(root, current))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("daily log Stat() error = %v, want not-exist", err)
	}
}

func TestKairosDateChangeAttachmentTail(t *testing.T) {
	cfg := &Config{Enabled: true, RootDir: t.TempDir(), ProjectRoot: newTestGitProjectRoot(t), Features: MemoryFeatureFlags{Kairos: true}}
	provider := mustNewContextProvider(t, cfg)
	current := time.Date(2026, 4, 15, 23, 55, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	provider.timeNow = func() time.Time { return current }

	first := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "remember this")
	if len(first.Attachments) != 0 {
		t.Fatalf("first attachments = %#v, want none", first.Attachments)
	}

	current = time.Date(2026, 4, 16, 0, 5, 0, 0, current.Location())
	second := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "remember this")
	if len(second.Attachments) != 1 {
		t.Fatalf("len(second.Attachments) = %d, want 1", len(second.Attachments))
	}
	attachment := second.Attachments[len(second.Attachments)-1]
	if attachment.Kind != dateChangeAttachmentKind {
		t.Fatalf("attachment kind = %q, want %q", attachment.Kind, dateChangeAttachmentKind)
	}
	for _, snippet := range []string{"logs/2026/04/2026-04-16.md", "2026-04-16"} {
		if !strings.Contains(attachment.Path+"\n"+attachment.Content, snippet) {
			t.Fatalf("attachment missing %q: %#v", snippet, attachment)
		}
	}

	third := provider.PrepareTurnContext(context.Background(), historyStubSession{}, contract.BuildCtx{}, "thread-1", "remember this")
	if len(third.Attachments) != 0 {
		t.Fatalf("third attachments = %#v, want none after same-day repeat", third.Attachments)
	}
}
