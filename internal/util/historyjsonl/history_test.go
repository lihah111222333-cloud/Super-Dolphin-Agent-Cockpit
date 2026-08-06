package historyjsonl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestDefaultDiscoveryOpsReturnsIndependentDescriptors(t *testing.T) {
	t.Parallel()
	first := newDefaultDiscoveryOps()
	second := newDefaultDiscoveryOps()
	first.stat = nil
	if second.stat == nil || second.walkDir == nil {
		t.Fatal("default discovery operations unexpectedly share mutable state")
	}
}

func TestCodexHistoryFallsBackToSessionUUID(t *testing.T) {
	root := t.TempDir()
	sessionUUID := "019e2c22-abae-7e12-9348-1f910b2052e1"
	writeCodexRollout(t, root, sessionUUID, "12345678910已完成。")

	messages, err := ReadProviderMessages(ReadRequest{
		Provider:    "codex",
		ThreadID:    "agent_1778856929920777000",
		SessionUUID: sessionUUID,
		CodexHome:   root,
	})
	if err != nil {
		t.Fatalf("ReadProviderMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ReadProviderMessages() len = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "12345678910已完成。" {
		t.Fatalf("message content = %q, want session UUID rollout", got)
	}
}

func TestCodexHistoryPrefersProviderThreadIDBeforeSessionUUID(t *testing.T) {
	root := t.TempDir()
	writeCodexRollout(t, root, "provider-thread-id", "provider result")
	writeCodexRollout(t, root, "session-uuid", "session result")

	messages, err := ReadProviderMessages(ReadRequest{
		Provider:         "codex",
		ThreadID:         "agent_1",
		ProviderThreadID: "provider-thread-id",
		SessionUUID:      "session-uuid",
		CodexHome:        root,
	})
	if err != nil {
		t.Fatalf("ReadProviderMessages() error = %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ReadProviderMessages() len = %d, want 1", len(messages))
	}
	if got := messages[0].Content; got != "provider result" {
		t.Fatalf("message content = %q, want provider_thread_id rollout", got)
	}
}

func TestMissingProviderHistoryUsesSentinelNotStringMatching(t *testing.T) {
	if !IsMissingProviderHistory(fmt.Errorf("wrapped: %w", errProviderHistoryNotFound)) {
		t.Fatal("wrapped missing sentinel should be treated as missing history")
	}
	if IsMissingProviderHistory(errors.New("persisted thread history not found")) {
		t.Fatal("plain matching error string should not be treated as missing history")
	}
}

func TestReadProviderMessagesIfExistsTreatsMissingPathAsOptional(t *testing.T) {
	_, ok, err := ReadProviderMessagesIfExists(ReadRequest{Provider: "codex", RolloutPath: filepath.Join(t.TempDir(), "missing.jsonl")})
	if err != nil {
		t.Fatalf("ReadProviderMessagesIfExists() error = %v", err)
	}
	if ok {
		t.Fatal("ReadProviderMessagesIfExists() ok = true, want false for missing file")
	}
}

func TestReadProviderMessagesPageOrErrorTreatsMissingPathAsMissingHistory(t *testing.T) {
	missingErr := errors.New("live session unavailable")
	_, err := ReadProviderMessagesPageOrError(
		ReadRequest{Provider: "codex", RolloutPath: filepath.Join(t.TempDir(), "missing.jsonl")},
		dto.MessagePageRequest{Limit: 10},
		missingErr,
	)
	if !errors.Is(err, missingErr) {
		t.Fatalf("ReadProviderMessagesPageOrError() error = %v, want %v", err, missingErr)
	}
}

func TestReadProviderMessagesPageReturnsRecentMessagesAndCursor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeCodexRolloutLines(t, root, "thread-page", []string{"one", "two", "three", "four"})

	first, err := ReadProviderMessagesPage(ReadRequest{Provider: "codex", RolloutPath: path}, dto.MessagePageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(first) error = %v", err)
	}
	requireHistoryPageContents(t, first.Messages, []string{"three", "four"})
	requireHistoryPageCursor(t, first)

	second, err := ReadProviderMessagesPage(ReadRequest{Provider: "codex", RolloutPath: path}, dto.MessagePageRequest{Limit: 2, Before: first.NextBefore})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(second) error = %v", err)
	}
	requireHistoryPageContents(t, second.Messages, []string{"one", "two"})
	requireHistoryPageIDsDoNotOverlap(t, first.Messages, second.Messages)
	requireHistoryPageEnd(t, second)
}

func TestReadProviderMessagesPageEmptySuccessHasSourceRevision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeCodexRolloutLines(t, root, "thread-empty-page", nil)
	page, err := ReadProviderMessagesPage(
		ReadRequest{Provider: "codex", RolloutPath: path},
		dto.MessagePageRequest{Limit: 10},
	)
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage() error = %v", err)
	}
	if len(page.Messages) != 0 || page.HasMore || page.NextBefore != "" {
		t.Fatalf("empty page = messages:%#v hasMore:%v nextBefore:%q", page.Messages, page.HasMore, page.NextBefore)
	}
	if page.SourceRevision == "" {
		t.Fatal("empty successful page SourceRevision is empty")
	}
}

func TestMessagePageSourceRevisionChangesWhenJSONLAppends(t *testing.T) {
	root := t.TempDir()
	path := writeCodexRolloutLines(t, root, "thread-source-revision", []string{"first", "duplicate-secret"})
	req := ReadRequest{Provider: "codex", RolloutPath: path}

	beforeAppend, err := ReadProviderMessagesPage(req, dto.MessagePageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(before append) error = %v", err)
	}
	if beforeAppend.SourceRevision == "" {
		t.Fatal("SourceRevision before append is empty")
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(append) error = %v", err)
	}
	if _, err := file.WriteString(codexRolloutMessageLine(31, "user", "input_text", "duplicate-secret")); err != nil {
		_ = file.Close()
		t.Fatalf("WriteString(append) error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close(append) error = %v", err)
	}

	afterAppend, err := ReadProviderMessagesPage(req, dto.MessagePageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(after append) error = %v", err)
	}
	if afterAppend.SourceRevision == "" {
		t.Fatal("SourceRevision after append is empty")
	}
	if afterAppend.SourceRevision == beforeAppend.SourceRevision {
		t.Fatalf("SourceRevision did not change after same-second duplicate append: %q", afterAppend.SourceRevision)
	}
}

func TestMessagePageSourceRevisionStableAcrossPagesAndDoesNotExposePathOrContent(t *testing.T) {
	root := t.TempDir()
	const secret = "prompt-that-must-not-leak"
	path := writeCodexRolloutLines(t, root, "thread-source-revision-private", []string{"one", "two", secret, "four"})
	req := ReadRequest{Provider: "codex", RolloutPath: path}

	first, err := ReadProviderMessagesPage(req, dto.MessagePageRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(first) error = %v", err)
	}
	second, err := ReadProviderMessagesPage(req, dto.MessagePageRequest{Limit: 2, Before: first.NextBefore})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage(second) error = %v", err)
	}
	if first.SourceRevision == "" || second.SourceRevision == "" {
		t.Fatalf("SourceRevision must be non-empty on every page: first=%q second=%q", first.SourceRevision, second.SourceRevision)
	}
	if first.SourceRevision != second.SourceRevision {
		t.Fatalf("SourceRevision changed across one source snapshot: first=%q second=%q", first.SourceRevision, second.SourceRevision)
	}
	for _, forbidden := range []string{root, path, secret} {
		if strings.Contains(first.SourceRevision, forbidden) {
			t.Fatalf("SourceRevision exposes source detail %q", forbidden)
		}
	}
}

func TestReadProviderMessagesPageDropsTurnAbortedControlBlock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeCodexRolloutRawLines(t, root, "thread-aborted-control", []string{
		codexRolloutMessageLine(30, "user", "input_text", "visible prompt"),
		codexRolloutMessageLine(31, "user", "input_text", "<turn_aborted>\nThe user interrupted the previous turn on purpose. Any running unified exec processes may still be running in the background. If any tools/commands were aborted, they may have partially executed.\n</turn_aborted>"),
		codexRolloutMessageLine(32, "assistant", "output_text", "visible reply"),
	})

	page, err := ReadProviderMessagesPage(ReadRequest{Provider: "codex", RolloutPath: path}, dto.MessagePageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage() error = %v", err)
	}
	requireHistoryPageContents(t, page.Messages, []string{"visible prompt", "visible reply"})
}

func TestReadProviderMessagesPageDropsEnvironmentContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := writeCodexRolloutRawLines(t, root, "thread-env-context", []string{
		codexRolloutMessageLine(30, "user", "input_text", "<environment_context>\n<cwd>/Users/mac/Desktop/Super-Dolphin</cwd>\n</environment_context>\nvisible prompt"),
		codexRolloutMessageLine(32, "assistant", "output_text", "visible reply"),
	})

	page, err := ReadProviderMessagesPage(ReadRequest{Provider: "codex", RolloutPath: path}, dto.MessagePageRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ReadProviderMessagesPage() error = %v", err)
	}
	requireHistoryPageContents(t, page.Messages, []string{"visible prompt", "visible reply"})
}

func requireHistoryPageContents(t *testing.T, messages []dto.Message, want []string) {
	t.Helper()
	got := make([]string, 0, len(messages))
	for _, msg := range messages {
		got = append(got, msg.Content)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("page contents = %#v, want %#v", got, want)
	}
}

func requireHistoryPageCursor(t *testing.T, page dto.MessagePageResult) {
	t.Helper()
	if !page.HasMore {
		t.Fatal("hasMore = false, want true")
	}
	if page.NextBefore == "" || page.NextBefore == "3" {
		t.Fatalf("nextBefore = %q, want opaque cursor", page.NextBefore)
	}
}

func requireHistoryPageEnd(t *testing.T, page dto.MessagePageResult) {
	t.Helper()
	if page.HasMore {
		t.Fatal("hasMore = true, want false")
	}
	if page.NextBefore != "" {
		t.Fatalf("nextBefore = %q, want empty", page.NextBefore)
	}
}

func requireHistoryPageIDsDoNotOverlap(t *testing.T, first, second []dto.Message) {
	t.Helper()
	seen := make(map[int64]string, len(first))
	for _, msg := range first {
		if msg.ID == 0 {
			t.Fatalf("first page message %q has zero ID", msg.Content)
		}
		seen[msg.ID] = msg.Content
	}
	for _, msg := range second {
		if msg.ID == 0 {
			t.Fatalf("second page message %q has zero ID", msg.Content)
		}
		if previous, ok := seen[msg.ID]; ok {
			t.Fatalf("message ID %d reused by %q and %q", msg.ID, previous, msg.Content)
		}
	}
}

func writeCodexRollout(t *testing.T, root, id, content string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", "2026", "05", "15", "rollout-2026-05-15T22-55-30-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	raw := `{"timestamp":"2026-05-15T22:55:30Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` + content + `"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func codexRolloutMessageLine(second int, role, itemType, content string) string {
	return fmt.Sprintf(
		`{"timestamp":"2026-05-15T22:55:%02dZ","type":"response_item","payload":{"type":"message","role":%q,"content":[{"type":%q,"text":%q}]}}`+"\n",
		second,
		role,
		itemType,
		content,
	)
}

func writeCodexRolloutRawLines(t *testing.T, root, id string, lines []string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", "2026", "05", "15", "rollout-2026-05-15T22-55-30-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	var raw string
	for _, line := range lines {
		raw += line
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func writeCodexRolloutLines(t *testing.T, root, id string, contents []string) string {
	t.Helper()
	path := filepath.Join(root, "sessions", "2026", "05", "15", "rollout-2026-05-15T22-55-30-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	var raw string
	for i, content := range contents {
		raw += fmt.Sprintf(
			`{"timestamp":"2026-05-15T22:55:%02dZ","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"%s"}]}}`+"\n",
			i+30,
			content,
		)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
