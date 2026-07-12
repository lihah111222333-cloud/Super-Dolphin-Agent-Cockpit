package thread

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestScanPromptHistoryWiresExactCWDAndActiveFirst(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	activeStamp := time.Unix(2, 0).UTC()
	otherStamp := time.Unix(1, 0).UTC()
	active := &historyTestSession{threadID: "thread-active", page: providerdto.MessagePageResult{
		Messages:       []providerdto.Message{{ID: 2, Role: "user", Content: "active prompt", Timestamp: activeStamp}},
		SourceRevision: "revision-active",
	}}
	other := &historyTestSession{threadID: "thread-other", page: providerdto.MessagePageResult{
		Messages:       []providerdto.Message{{ID: 1, Role: "user", Content: "other prompt", Timestamp: otherStamp}},
		SourceRevision: "revision-other",
	}}
	prefix := &historyTestSession{threadID: "thread-prefix", page: providerdto.MessagePageResult{
		Messages:       []providerdto.Message{{ID: 9, Role: "user", Content: "must not appear"}},
		SourceRevision: "revision-prefix",
	}}
	svc := &service{
		threadStore: &stubThreadStore{threads: []ThreadRecord{
			{ThreadID: "thread-other", Cwd: cwd, Status: "created", UpdatedAt: 20},
			{ThreadID: "thread-active", Cwd: cwd, Status: "running", UpdatedAt: 1},
			{ThreadID: "thread-prefix", Cwd: cwd + "-other", Status: "created", UpdatedAt: 30},
		}},
		sessions: &historyTestSessionProvider{sessions: map[string]contract.Session{
			"thread-active": active, "thread-other": other, "thread-prefix": prefix,
		}},
		threadAgents: map[string]string{
			"thread-active": "thread-active", "thread-other": "thread-other", "thread-prefix": "thread-prefix",
		},
	}

	result, err := svc.ScanPromptHistory(context.Background(), PromptHistoryRequest{
		CWD: cwd, ActiveThreadID: "thread-active", Limit: 10,
	})
	if err != nil {
		t.Fatalf("ScanPromptHistory() error = %v", err)
	}
	requirePromptHistoryParentResult(t, result, activeStamp, otherStamp)
	requirePromptHistoryParentReads(t, active, other, prefix)
}

func requirePromptHistoryParentResult(t *testing.T, result threaddto.PromptHistoryResult, activeStamp, otherStamp time.Time) {
	t.Helper()
	wantEntries := []threaddto.PromptHistoryEntry{
		{ThreadID: "thread-active", MessageID: "2", Text: "active prompt", CreatedAt: activeStamp},
		{ThreadID: "thread-other", MessageID: "1", Text: "other prompt", CreatedAt: otherStamp},
	}
	if !reflect.DeepEqual(result.Entries, wantEntries) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, wantEntries)
	}
	if result.Nonce != "5bb39a0a33e9bfc67ee676f4dc225b01483513ea8f2a1602497a70c341aeb884" {
		t.Fatalf("nonce = %q, want exact snapshot golden", result.Nonce)
	}
	if result.HasMore || result.NextCursor != "" {
		t.Fatalf("metadata = hasMore:%v nextCursor:%q, want exhausted", result.HasMore, result.NextCursor)
	}
}

func requirePromptHistoryParentReads(t *testing.T, active, other, prefix *historyTestSession) {
	t.Helper()
	if len(active.pageCalls) != 2 || len(other.pageCalls) != 2 {
		t.Fatalf("exact pager calls = active:%d other:%d, want 2/2", len(active.pageCalls), len(other.pageCalls))
	}
	if len(active.readCalls) != 0 || len(other.readCalls) != 0 || len(prefix.pageCalls) != 0 || len(prefix.readCalls) != 0 {
		t.Fatalf("unexpected legacy/prefix reads = active:%d other:%d prefix page/read:%d/%d",
			len(active.readCalls), len(other.readCalls), len(prefix.pageCalls), len(prefix.readCalls))
	}
}

func TestScanPromptHistoryRejectsActiveThreadOutsideExactCWDBeforeRead(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	exactSession := &historyTestSession{threadID: "thread-exact"}
	prefixSession := &historyTestSession{threadID: "thread-prefix"}
	svc := &service{
		threadStore: &stubThreadStore{threads: []ThreadRecord{
			{ThreadID: "thread-exact", Cwd: cwd, Status: "created", UpdatedAt: 2},
			{ThreadID: "thread-prefix", Cwd: cwd + "-other", Status: "created", UpdatedAt: 1},
		}},
		sessions: &historyTestSessionProvider{sessions: map[string]contract.Session{
			"thread-exact":  exactSession,
			"thread-prefix": prefixSession,
		}},
		threadAgents: map[string]string{
			"thread-exact":  "thread-exact",
			"thread-prefix": "thread-prefix",
		},
	}

	_, err := svc.ScanPromptHistory(context.Background(), PromptHistoryRequest{
		CWD:            cwd + string(os.PathSeparator) + ".",
		ActiveThreadID: "thread-prefix",
		Limit:          10,
	})
	if !errors.Is(err, ErrPromptHistoryActiveThreadCWD) {
		t.Fatalf("ScanPromptHistory() error = %v, want ErrPromptHistoryActiveThreadCWD", err)
	}
	if len(exactSession.pageCalls) != 0 || len(exactSession.readCalls) != 0 || len(prefixSession.pageCalls) != 0 || len(prefixSession.readCalls) != 0 {
		t.Fatalf("message readers called before exact-cwd rejection: exact page/read=%d/%d prefix page/read=%d/%d",
			len(exactSession.pageCalls), len(exactSession.readCalls), len(prefixSession.pageCalls), len(prefixSession.readCalls))
	}
	if strings.Contains(err.Error(), cwd) {
		t.Fatalf("exact-cwd rejection exposes cwd: %v", err)
	}
}

func TestScanPromptHistoryRejectsLegacyReadHistoryWithoutRevisionBeforeRead(t *testing.T) {
	t.Parallel()

	cwd := t.TempDir()
	legacy := &promptHistoryLegacySession{
		threadID: "thread-legacy",
		messages: []providerdto.Message{{ID: 1, Role: "user", Content: "prompt-secret"}},
	}
	svc := &service{
		threadStore: &stubThreadStore{thread: &ThreadRecord{
			ThreadID:  "thread-legacy",
			Cwd:       cwd,
			Status:    "created",
			UpdatedAt: 1,
		}},
		sessions: &historyTestSessionProvider{sessions: map[string]contract.Session{
			"thread-legacy": legacy,
		}},
		threadAgents: map[string]string{"thread-legacy": "thread-legacy"},
	}

	_, err := svc.ScanPromptHistory(context.Background(), PromptHistoryRequest{CWD: cwd, Limit: 10})
	if !errors.Is(err, ErrPromptHistoryRevisionUnavailable) {
		t.Fatalf("ScanPromptHistory() error = %v, want ErrPromptHistoryRevisionUnavailable", err)
	}
	if legacy.readCalls != 0 {
		t.Fatalf("legacy ReadHistory calls = %d, want 0", legacy.readCalls)
	}
	for _, forbidden := range []string{cwd, "prompt-secret"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("revision error exposes source detail %q: %v", forbidden, err)
		}
	}
}

type promptHistoryLegacySession struct {
	historyTestSessionUnusedMethods

	threadID  string
	messages  []providerdto.Message
	readCalls int
}

func (s *promptHistoryLegacySession) ThreadID() string { return s.threadID }

func (s *promptHistoryLegacySession) ListThreads(context.Context) ([]providerdto.ThreadRef, error) {
	return nil, nil
}

func (s *promptHistoryLegacySession) ForkThread(context.Context, providerdto.ForkRequest) (providerdto.ForkResult, error) {
	return providerdto.ForkResult{}, nil
}

func (s *promptHistoryLegacySession) ReadHistory(context.Context, string, int) ([]providerdto.Message, error) {
	s.readCalls++
	return append([]providerdto.Message(nil), s.messages...), nil
}
