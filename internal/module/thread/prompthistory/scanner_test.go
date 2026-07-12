package prompthistory

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestScanPromptHistoryActiveThreadFirstAndKeepsDuplicates(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	reader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"active": promptHistoryPage("rev-active",
			promptHistoryMessage(1, "user", "duplicate", stamp),
			promptHistoryMessage(9, "assistant", "not-a-prompt", stamp),
			promptHistoryMessage(2, "user", "duplicate", stamp),
			promptHistoryMessage(10, "tool", "not-a-prompt", stamp),
		),
		"thread-a": promptHistoryPage("rev-a", promptHistoryMessage(3, "user", "from-a", stamp)),
		"thread-b": promptHistoryPage("rev-b", promptHistoryMessage(4, "user", "from-b", stamp)),
	}}
	result, err := ScanPromptHistory(context.Background(), Request{
		Threads: []ThreadSnapshot{
			{ThreadID: "thread-b", Status: "created", UpdatedAt: 20},
			{ThreadID: "active", Status: "running", UpdatedAt: 1},
			{ThreadID: "thread-a", Status: "created", UpdatedAt: 20},
		},
		ActiveThreadID: "active",
		Limit:          10,
		ReadPage:       reader.readPage,
	})
	if err != nil {
		t.Fatalf("ScanPromptHistory() error = %v", err)
	}
	want := []threaddto.PromptHistoryEntry{
		{ThreadID: "active", MessageID: "2", Text: "duplicate", CreatedAt: stamp},
		{ThreadID: "active", MessageID: "1", Text: "duplicate", CreatedAt: stamp},
		{ThreadID: "thread-a", MessageID: "3", Text: "from-a", CreatedAt: stamp},
		{ThreadID: "thread-b", MessageID: "4", Text: "from-b", CreatedAt: stamp},
	}
	if fmt.Sprint(result.Entries) != fmt.Sprint(want) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, want)
	}
	if result.HasMore || result.NextCursor != "" {
		t.Fatalf("result metadata = hasMore:%v nextCursor:%q", result.HasMore, result.NextCursor)
	}
}

func TestScanPromptHistoryCursorCrossesThreadWithoutGap(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	reader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"newer": promptHistoryPage("rev-newer", promptHistoryMessage(1, "user", "newer", stamp)),
		"older": promptHistoryPage("rev-older", promptHistoryMessage(2, "user", "older", stamp)),
	}}
	request := Request{
		Threads: []ThreadSnapshot{
			{ThreadID: "older", Status: "created", UpdatedAt: 1},
			{ThreadID: "newer", Status: "created", UpdatedAt: 2},
		},
		Limit:    1,
		ReadPage: reader.readPage,
	}
	first, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(first) error = %v", err)
	}
	requireFirstCrossThreadPage(t, first)
	request.Cursor = first.NextCursor
	request.Nonce = first.Nonce
	second, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(second) error = %v", err)
	}
	requireSecondCrossThreadPage(t, second, first.Nonce)
	if first.Entries[0].MessageID == second.Entries[0].MessageID {
		t.Fatalf("cursor duplicated message ID %q", first.Entries[0].MessageID)
	}
}

func TestScanPromptHistoryCursorContinuesWithinThreadWithoutGap(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	reader := &promptHistoryReader{pagesByBefore: map[string]map[string]providerdto.MessagePageResult{
		"thread-1": {
			"": promptHistoryPagedResult("revision-stable", true, "older-page",
				promptHistoryMessage(2, "user", "latest", stamp),
			),
			"older-page": promptHistoryPagedResult("revision-stable", false, "",
				promptHistoryMessage(1, "user", "older", stamp),
			),
		},
	}}
	request := Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    1,
		ReadPage: reader.readPage,
	}
	first, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(first) error = %v", err)
	}
	requirePromptHistorySingleEntry(t, first, "2", "latest", true)
	request.Cursor = first.NextCursor
	request.Nonce = first.Nonce
	second, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(second) error = %v", err)
	}
	requirePromptHistorySingleEntry(t, second, "1", "older", false)
	if first.Entries[0].MessageID == second.Entries[0].MessageID {
		t.Fatalf("same-thread cursor duplicated message ID %q", first.Entries[0].MessageID)
	}
	requirePromptHistoryReadBefore(t, reader.calls, "thread-1", "older-page")
}

func TestScanPromptHistoryRejectsChangedRevisionOnLaterThreadPage(t *testing.T) {
	t.Parallel()

	const rawRevision = "raw-revision-must-not-leak"
	reader := &promptHistoryReader{pagesByBefore: map[string]map[string]providerdto.MessagePageResult{
		"thread-1": {
			"":           promptHistoryPagedResult("revision-initial", true, "older-page", promptHistoryMessage(2, "user", "latest", time.Unix(2, 0).UTC())),
			"older-page": promptHistoryPagedResult(rawRevision, false, "", promptHistoryMessage(1, "user", "older", time.Unix(1, 0).UTC())),
		},
	}}
	request := Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    1,
		ReadPage: reader.readPage,
	}
	first, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(first) error = %v", err)
	}
	request.Cursor = first.NextCursor
	request.Nonce = first.Nonce
	_, err = ScanPromptHistory(context.Background(), request)
	if !errors.Is(err, ErrStaleNonce) {
		t.Fatalf("ScanPromptHistory(revision mismatch) error = %v, want ErrStaleNonce", err)
	}
	if strings.Contains(fmt.Sprint(err), rawRevision) {
		t.Fatalf("revision mismatch error exposes raw revision: %v", err)
	}
	requirePromptHistoryReadBefore(t, reader.calls, "thread-1", "older-page")
}

func TestScanPromptHistorySkipsRawPageWithoutUserAndFindsOlderPrompt(t *testing.T) {
	t.Parallel()

	stamp := time.Unix(1, 0).UTC()
	reader := &promptHistoryReader{pagesByBefore: map[string]map[string]providerdto.MessagePageResult{
		"thread-1": {
			"": promptHistoryPagedResult("revision-stable", true, "older-page",
				promptHistoryMessage(3, "assistant", "assistant output", stamp),
				promptHistoryMessage(4, "tool", "tool output", stamp),
			),
			"older-page": promptHistoryPagedResult("revision-stable", false, "",
				promptHistoryMessage(2, "user", "older user prompt", stamp),
			),
		},
	}}
	result, err := ScanPromptHistory(context.Background(), Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    2,
		ReadPage: reader.readPage,
	})
	if err != nil {
		t.Fatalf("ScanPromptHistory() error = %v", err)
	}
	want := []threaddto.PromptHistoryEntry{{
		ThreadID: "thread-1", MessageID: "2", Text: "older user prompt", CreatedAt: stamp,
	}}
	if fmt.Sprint(result.Entries) != fmt.Sprint(want) {
		t.Fatalf("entries = %#v, want %#v", result.Entries, want)
	}
	if result.HasMore || result.NextCursor != "" {
		t.Fatalf("metadata = hasMore:%v nextCursor:%q, want exhausted", result.HasMore, result.NextCursor)
	}
	requirePromptHistoryReadBefore(t, reader.calls, "thread-1", "older-page")
}

func requirePromptHistorySingleEntry(t *testing.T, page threaddto.PromptHistoryResult, messageID, text string, hasMore bool) {
	t.Helper()
	if len(page.Entries) != 1 {
		t.Fatalf("entries = %#v, want one", page.Entries)
	}
	if page.Entries[0].MessageID != messageID {
		t.Fatalf("messageId = %q, want %q", page.Entries[0].MessageID, messageID)
	}
	if page.Entries[0].Text != text {
		t.Fatalf("text = %q, want %q", page.Entries[0].Text, text)
	}
	if page.HasMore != hasMore {
		t.Fatalf("hasMore = %v, want %v", page.HasMore, hasMore)
	}
	if hasMore && page.NextCursor == "" {
		t.Fatal("nextCursor is empty while hasMore is true")
	}
	if !hasMore && page.NextCursor != "" {
		t.Fatalf("nextCursor = %q, want empty", page.NextCursor)
	}
}

func requirePromptHistoryReadBefore(t *testing.T, calls []promptHistoryReadCall, threadID, before string) {
	t.Helper()
	for _, call := range calls {
		if call.threadID == threadID && call.request.Before == before {
			return
		}
	}
	t.Fatalf("ReadPage calls = %#v, want thread=%q before=%q", calls, threadID, before)
}

func requireFirstCrossThreadPage(t *testing.T, page threaddto.PromptHistoryResult) {
	t.Helper()
	if len(page.Entries) != 1 {
		t.Fatalf("first page entries = %#v, want one", page.Entries)
	}
	if page.Entries[0].ThreadID != "newer" {
		t.Fatalf("first page thread = %q, want newer", page.Entries[0].ThreadID)
	}
	if !page.HasMore {
		t.Fatalf("first page hasMore = false, want true")
	}
	if page.NextCursor == "" {
		t.Fatal("first page nextCursor is empty")
	}
	if page.Nonce == "" {
		t.Fatal("first page nonce is empty")
	}
}

func requireSecondCrossThreadPage(t *testing.T, page threaddto.PromptHistoryResult, nonce string) {
	t.Helper()
	if len(page.Entries) != 1 {
		t.Fatalf("second page entries = %#v, want one", page.Entries)
	}
	if page.Entries[0].ThreadID != "older" {
		t.Fatalf("second page thread = %q, want older", page.Entries[0].ThreadID)
	}
	if page.HasMore {
		t.Fatalf("second page hasMore = true, want false")
	}
	if page.NextCursor != "" {
		t.Fatalf("second page nextCursor = %q, want empty", page.NextCursor)
	}
	if page.Nonce != nonce {
		t.Fatalf("second page nonce = %q, want %q", page.Nonce, nonce)
	}
}

func TestScanPromptHistoryRejectsStaleNonceAndInvalidCursor(t *testing.T) {
	t.Parallel()

	reader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage("revision-1", promptHistoryMessage(1, "user", "secret-prompt", time.Unix(1, 0).UTC())),
	}}
	base := Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    1,
		ReadPage: reader.readPage,
	}
	first, err := ScanPromptHistory(context.Background(), base)
	if err != nil {
		t.Fatalf("ScanPromptHistory(first) error = %v", err)
	}
	tests := []struct {
		name    string
		cursor  string
		nonce   string
		wantErr error
	}{
		{name: "stale request nonce", nonce: "stale", wantErr: ErrStaleNonce},
		{
			name:    "stale cursor nonce",
			cursor:  encodePromptHistoryCursorForTest(`{"version":1,"nonce":"stale","threadIndex":0,"before":""}`),
			nonce:   first.Nonce,
			wantErr: ErrStaleNonce,
		},
		{
			name:    "unknown cursor version",
			cursor:  encodePromptHistoryCursorForTest(`{"version":2,"nonce":"` + first.Nonce + `","threadIndex":0,"before":""}`),
			nonce:   first.Nonce,
			wantErr: ErrInvalidCursor,
		},
		{
			name:    "unknown cursor field",
			cursor:  encodePromptHistoryCursorForTest(`{"version":1,"nonce":"` + first.Nonce + `","threadIndex":0,"before":"","extra":true}`),
			nonce:   first.Nonce,
			wantErr: ErrInvalidCursor,
		},
		{
			name:    "decoded cursor exceeds limit",
			cursor:  encodePromptHistoryCursorForTest(strings.Repeat("x", 2049)),
			nonce:   first.Nonce,
			wantErr: ErrInvalidCursor,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := base
			req.Cursor = tc.cursor
			req.Nonce = tc.nonce
			_, gotErr := ScanPromptHistory(context.Background(), req)
			if !errors.Is(gotErr, tc.wantErr) {
				t.Fatalf("ScanPromptHistory() error = %v, want %v", gotErr, tc.wantErr)
			}
			if strings.Contains(fmt.Sprint(gotErr), "secret-prompt") {
				t.Fatalf("error exposes prompt content: %v", gotErr)
			}
		})
	}
}

func TestPromptHistoryCursorEnforcesEncodedWireLimit(t *testing.T) {
	t.Parallel()
	normal := promptHistoryCursor{Version: promptHistoryCursorVersion, Nonce: strings.Repeat("n", 64), Before: "cursor"}
	wire, err := encodePromptHistoryCursor(normal)
	if err != nil {
		t.Fatalf("encodePromptHistoryCursor(normal) error = %v", err)
	}
	if _, err := decodePromptHistoryCursor(wire); err != nil {
		t.Fatalf("decodePromptHistoryCursor(normal) error = %v", err)
	}
	oversized := promptHistoryCursor{Version: promptHistoryCursorVersion, Nonce: strings.Repeat("n", 64), Before: strings.Repeat("b", 1900)}
	wire, err = encodePromptHistoryCursor(oversized)
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("encodePromptHistoryCursor(oversized) error = %v, wire bytes = %d", err, len(wire))
	}
	raw := encodePromptHistoryCursorForTest(`{"version":1,"nonce":"` + strings.Repeat("n", 64) + `","threadIndex":0,"before":"` + strings.Repeat("b", 1500) + `"}`)
	if len(raw) <= maxPromptHistoryCursorBytes {
		t.Fatalf("test cursor bytes = %d, want > %d", len(raw), maxPromptHistoryCursorBytes)
	}
	if _, err := decodePromptHistoryCursor(raw); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("decodePromptHistoryCursor(oversized wire) error = %v", err)
	}
}

func TestPromptHistoryNonceChangesForSameSecondDuplicateAndSnapshotLifecycle(t *testing.T) {
	t.Parallel()

	stamp := time.Date(2026, 7, 12, 1, 2, 3, 0, time.UTC)
	baseThread := ThreadSnapshot{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}
	basePage := promptHistoryPage("revision-1", promptHistoryMessage(1, "user", "duplicate-secret", stamp))
	baseNonce := scanPromptHistoryNonce(t, []ThreadSnapshot{baseThread}, map[string]providerdto.MessagePageResult{"thread-1": basePage})

	duplicateNonce := scanPromptHistoryNonce(t, []ThreadSnapshot{baseThread}, map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage("revision-2",
			promptHistoryMessage(1, "user", "duplicate-secret", stamp),
			promptHistoryMessage(2, "user", "duplicate-secret", stamp),
		),
	})
	revisionNonce := scanPromptHistoryNonce(t, []ThreadSnapshot{baseThread}, map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage("revision-2", basePage.Messages...),
	})
	archivedNonce := scanPromptHistoryNonce(t, []ThreadSnapshot{{ThreadID: "thread-1", Status: "archived", UpdatedAt: 1}}, map[string]providerdto.MessagePageResult{"thread-1": basePage})
	createdNonce := scanPromptHistoryNonce(t, []ThreadSnapshot{
		baseThread,
		{ThreadID: "thread-2", Status: "created", UpdatedAt: 2},
	}, map[string]providerdto.MessagePageResult{
		"thread-1": basePage,
		"thread-2": promptHistoryPage("revision-new"),
	})
	deletedNonce := scanPromptHistoryNonce(t, nil, nil)

	for name, nonce := range map[string]string{
		"same-second duplicate": duplicateNonce,
		"source revision":       revisionNonce,
		"archive":               archivedNonce,
		"create":                createdNonce,
		"delete":                deletedNonce,
	} {
		if nonce == "" || nonce == baseNonce {
			t.Fatalf("%s nonce = %q, must differ from baseline %q", name, nonce, baseNonce)
		}
		for _, forbidden := range []string{"duplicate-secret", "revision-1", "revision-2"} {
			if strings.Contains(nonce, forbidden) {
				t.Fatalf("%s nonce exposes source detail %q", name, forbidden)
			}
		}
	}
}

func TestScanPromptHistoryRejectsMoreThanOneHundredThreadsBeforeRead(t *testing.T) {
	t.Parallel()

	threads := make([]ThreadSnapshot, 101)
	for i := range threads {
		threads[i] = ThreadSnapshot{ThreadID: fmt.Sprintf("thread-%03d", i), Status: "created", UpdatedAt: int64(i)}
	}
	reader := &promptHistoryReader{}
	_, err := ScanPromptHistory(context.Background(), Request{Threads: threads, Limit: 1, ReadPage: reader.readPage})
	if !errors.Is(err, ErrThreadLimitExceeded) {
		t.Fatalf("ScanPromptHistory() error = %v, want ErrThreadLimitExceeded", err)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("page read count for oversized snapshot = %d, want 0", len(reader.calls))
	}
}

func TestScanPromptHistoryStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage("revision-1"),
	}}
	_, err := ScanPromptHistory(ctx, Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    1,
		ReadPage: reader.readPage,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanPromptHistory() error = %v, want context.Canceled", err)
	}
	if len(reader.calls) != 0 {
		t.Fatalf("page read count after cancellation = %d, want 0", len(reader.calls))
	}
}

func TestScanPromptHistoryReaderAndMismatchErrorsDoNotExposeSourceDetails(t *testing.T) {
	t.Parallel()

	const (
		secretPrompt   = "prompt-secret"
		secretCWD      = "/private/repository/path"
		secretRevision = "raw-revision-secret"
	)
	reader := &promptHistoryReader{err: fmt.Errorf("reader failed: %s %s %s", secretPrompt, secretCWD, secretRevision)}
	request := Request{
		Threads:  []ThreadSnapshot{{ThreadID: "thread-1", Status: "created", UpdatedAt: 1}},
		Limit:    1,
		ReadPage: reader.readPage,
	}
	_, err := ScanPromptHistory(context.Background(), request)
	if !errors.Is(err, ErrPageRead) {
		t.Fatalf("ScanPromptHistory(reader error) = %v, want ErrPageRead", err)
	}
	requirePromptHistoryErrorPrivate(t, err, secretPrompt, secretCWD, secretRevision)

	baselineReader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage("revision-1", promptHistoryMessage(1, "user", secretPrompt, time.Unix(1, 0).UTC())),
	}}
	request.ReadPage = baselineReader.readPage
	baseline, err := ScanPromptHistory(context.Background(), request)
	if err != nil {
		t.Fatalf("ScanPromptHistory(baseline) error = %v", err)
	}
	mismatchReader := &promptHistoryReader{pages: map[string]providerdto.MessagePageResult{
		"thread-1": promptHistoryPage(secretRevision, promptHistoryMessage(1, "user", secretPrompt, time.Unix(1, 0).UTC())),
	}}
	request.ReadPage = mismatchReader.readPage
	request.Nonce = baseline.Nonce
	_, err = ScanPromptHistory(context.Background(), request)
	if !errors.Is(err, ErrStaleNonce) {
		t.Fatalf("ScanPromptHistory(mismatch) = %v, want ErrStaleNonce", err)
	}
	requirePromptHistoryErrorPrivate(t, err, secretPrompt, secretCWD, secretRevision)
}

type promptHistoryReader struct {
	pages         map[string]providerdto.MessagePageResult
	pagesByBefore map[string]map[string]providerdto.MessagePageResult
	err           error
	calls         []promptHistoryReadCall
}

type promptHistoryReadCall struct {
	threadID string
	request  providerdto.MessagePageRequest
}

func (r *promptHistoryReader) readPage(_ context.Context, threadID string, req providerdto.MessagePageRequest) (providerdto.MessagePageResult, error) {
	r.calls = append(r.calls, promptHistoryReadCall{threadID: threadID, request: req})
	if r.err != nil {
		return providerdto.MessagePageResult{}, r.err
	}
	page, ok := r.promptHistoryPageForRequest(threadID, req.Before)
	if !ok {
		return providerdto.MessagePageResult{}, fmt.Errorf("missing test page for thread identity")
	}
	page.Messages = append([]providerdto.Message(nil), page.Messages...)
	return page, nil
}

func (r *promptHistoryReader) promptHistoryPageForRequest(threadID, before string) (providerdto.MessagePageResult, bool) {
	if byBefore, ok := r.pagesByBefore[threadID]; ok {
		page, found := byBefore[before]
		return page, found
	}
	page, ok := r.pages[threadID]
	return page, ok
}

func promptHistoryPage(revision string, messages ...providerdto.Message) providerdto.MessagePageResult {
	return providerdto.MessagePageResult{Messages: messages, SourceRevision: revision}
}

func promptHistoryPagedResult(revision string, hasMore bool, nextBefore string, messages ...providerdto.Message) providerdto.MessagePageResult {
	return providerdto.MessagePageResult{
		Messages:       messages,
		HasMore:        hasMore,
		NextBefore:     nextBefore,
		SourceRevision: revision,
	}
}

func promptHistoryMessage(id int64, role, content string, createdAt time.Time) providerdto.Message {
	return providerdto.Message{ID: id, Role: role, Content: content, Timestamp: createdAt}
}

func scanPromptHistoryNonce(t *testing.T, threads []ThreadSnapshot, pages map[string]providerdto.MessagePageResult) string {
	t.Helper()
	reader := &promptHistoryReader{pages: pages}
	result, err := ScanPromptHistory(context.Background(), Request{Threads: threads, Limit: 50, ReadPage: reader.readPage})
	if err != nil {
		t.Fatalf("ScanPromptHistory() error = %v", err)
	}
	if result.Nonce == "" {
		t.Fatal("ScanPromptHistory() nonce is empty")
	}
	return result.Nonce
}

func encodePromptHistoryCursorForTest(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func requirePromptHistoryErrorPrivate(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	text := fmt.Sprint(err)
	for _, value := range forbidden {
		if strings.Contains(text, value) {
			t.Fatalf("error exposes source detail %q: %v", value, err)
		}
	}
}
