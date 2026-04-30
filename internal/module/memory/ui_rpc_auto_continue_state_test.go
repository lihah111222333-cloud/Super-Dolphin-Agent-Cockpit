package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

// fakeSharedFileStore implements Reader + Upserter + Deleter against an
// in-memory map. Sufficient for Phase 1.6 RPC validation tests; no SQL
// required.
type fakeSharedFileStore struct {
	mu    sync.Mutex
	files map[string]*sharedfilestore.SharedFile
}

func newFakeSharedFileStore() *fakeSharedFileStore {
	return &fakeSharedFileStore{files: map[string]*sharedfilestore.SharedFile{}}
}

func (f *fakeSharedFileStore) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if item, ok := f.files[path]; ok {
		copy := *item
		return &copy, nil
	}
	return nil, errors.New("shared file not found: " + path)
}

func (f *fakeSharedFileStore) List(_ context.Context, _ sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (f *fakeSharedFileStore) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC()
	item := &sharedfilestore.SharedFile{
		Path:      params.Path,
		Content:   params.Content,
		UpdatedBy: params.UpdatedBy,
		UpdatedAt: now,
	}
	if existing, ok := f.files[params.Path]; ok {
		item.CreatedAt = existing.CreatedAt
	} else {
		item.CreatedAt = now
	}
	f.files[params.Path] = item
	copy := *item
	return &copy, nil
}

func (f *fakeSharedFileStore) Delete(_ context.Context, path string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[path]; !ok {
		return 0, nil
	}
	delete(f.files, path)
	return 1, nil
}

func newAutoContinueDeps() (memoryHandlerDeps, *fakeSharedFileStore) {
	store := newFakeSharedFileStore()
	return memoryHandlerDeps{
		SharedFiles:         store,
		SharedFilesDeleter:  store,
		SharedFilesUpserter: store,
	}, store
}

const validACSContent = `{"schemaVersion":1,"threadId":"thread_abc","manualAbortAt":null,"watchdogPokeCount":3}`

func TestValidateAutoContinueStatePath(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantTID string
		wantErr string // substring; empty => expect success
	}{
		{"happy", "_internal/auto-continue/state/thread_abc.json", "thread_abc", ""},
		{"empty", "", "", "path is required"},
		{"only spaces", "   ", "", "path is required"},
		{"no prefix", "handoff/tasks/thread_abc.json", "", "path must be under _internal/auto-continue/state/"},
		{"path traversal", "_internal/auto-continue/state/../etc/passwd.json", "", "path traversal not allowed"},
		{"backslash", "_internal/auto-continue/state/thread\\abc.json", "", "path contains invalid characters"},
		{"null byte", "_internal/auto-continue/state/thread\x00abc.json", "", "path contains invalid characters"},
		{"missing .json", "_internal/auto-continue/state/thread_abc", "", "path must end with .json"},
		{"empty threadId segment", "_internal/auto-continue/state/.json", "", "threadId segment is empty"},
		{"threadId with slash", "_internal/auto-continue/state/sub/thread_abc.json", "", "threadId segment must not contain /"},
		{"threadId starts with dot", "_internal/auto-continue/state/.hidden.json", "", "threadId segment has invalid characters"},
		{"threadId starts with dash", "_internal/auto-continue/state/-bad.json", "", "threadId segment has invalid characters"},
		{"threadId hyphenated", "_internal/auto-continue/state/thread-123_abc.json", "thread-123_abc", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleaned, tid, err := validateAutoContinueStatePath(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got err=%v", err)
				}
				if tid != tc.wantTID {
					t.Fatalf("threadId=%q, want %q", tid, tc.wantTID)
				}
				if cleaned != strings.TrimSpace(tc.path) {
					t.Fatalf("cleaned=%q, want %q", cleaned, tc.path)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected err containing %q, got ok (tid=%q)", tc.wantErr, tid)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateAutoContinueStateContent(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		threadID string
		wantErr  string
	}{
		{"happy", validACSContent, "thread_abc", ""},
		{"empty", "", "thread_abc", "content is required"},
		{"whitespace only", "  \n ", "thread_abc", "content is required"},
		{"not json", "not json at all", "thread_abc", "content is not valid JSON"},
		{"wrong schema version", `{"schemaVersion":2,"threadId":"thread_abc"}`, "thread_abc", "schemaVersion must be 1"},
		{"missing schema version", `{"threadId":"thread_abc"}`, "thread_abc", "schemaVersion must be 1"},
		{"threadId mismatch", `{"schemaVersion":1,"threadId":"other"}`, "thread_abc", "payload threadId must match path threadId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAutoContinueStateContent(tc.content, tc.threadID)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got err=%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected err %q, got ok", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestUpsertAutoContinueStateRoundTrip(t *testing.T) {
	deps, store := newAutoContinueDeps()
	ctx := context.Background()
	path := "_internal/auto-continue/state/thread_abc.json"

	// Upsert
	upserted, err := upsertAutoContinueState(ctx, deps, uiAutoContinueStateUpsertParams{
		Path:     path,
		ThreadID: "thread_abc",
		Content:  validACSContent,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upserted.Path != path || upserted.ThreadID != "thread_abc" {
		t.Fatalf("unexpected upsert detail: %+v", upserted)
	}
	if upserted.UpdatedBy != autoContinueStateUpdatedBy {
		t.Fatalf("updatedBy=%q, want %q", upserted.UpdatedBy, autoContinueStateUpdatedBy)
	}
	if upserted.Content != validACSContent {
		t.Fatalf("content roundtrip mismatch")
	}

	// Get
	got, err := getAutoContinueState(ctx, deps, uiAutoContinueStateGetParams{Path: path})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ThreadID != "thread_abc" || got.Content != validACSContent {
		t.Fatalf("get returned %+v", got)
	}

	// Delete
	deleted, err := deleteAutoContinueState(ctx, deps, uiAutoContinueStateDeleteParams{Path: path})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !deleted {
		t.Fatal("expected deleted=true")
	}
	if _, ok := store.files[path]; ok {
		t.Fatalf("store still contains %q after delete", path)
	}

	// Delete again -> false (not error)
	deleted2, err := deleteAutoContinueState(ctx, deps, uiAutoContinueStateDeleteParams{Path: path})
	if err != nil {
		t.Fatalf("second delete returned err: %v", err)
	}
	if deleted2 {
		t.Fatal("expected deleted=false on second delete")
	}
}

func TestUpsertAutoContinueStateRejectsThreadIDMismatch(t *testing.T) {
	deps, _ := newAutoContinueDeps()
	_, err := upsertAutoContinueState(context.Background(), deps, uiAutoContinueStateUpsertParams{
		Path:     "_internal/auto-continue/state/thread_abc.json",
		ThreadID: "thread_zzz",
		Content:  validACSContent,
	})
	if err == nil {
		t.Fatal("expected error on threadId mismatch")
	}
	if !strings.Contains(err.Error(), "threadId must match path threadId") {
		t.Fatalf("err=%q", err)
	}
}

func TestUpsertAutoContinueStateRejectsBadPath(t *testing.T) {
	deps, _ := newAutoContinueDeps()
	_, err := upsertAutoContinueState(context.Background(), deps, uiAutoContinueStateUpsertParams{
		Path:     "handoff/tasks/thread_abc.md",
		ThreadID: "thread_abc",
		Content:  validACSContent,
	})
	if err == nil {
		t.Fatal("expected path validation error")
	}
	if !strings.Contains(err.Error(), "path must be under _internal/auto-continue/state/") {
		t.Fatalf("err=%q", err)
	}
}

func TestUpsertAutoContinueStateRejectsMissingThreadID(t *testing.T) {
	deps, _ := newAutoContinueDeps()
	_, err := upsertAutoContinueState(context.Background(), deps, uiAutoContinueStateUpsertParams{
		Path:     "_internal/auto-continue/state/thread_abc.json",
		ThreadID: "",
		Content:  validACSContent,
	})
	if err == nil {
		t.Fatal("expected error on empty threadId")
	}
	if !strings.Contains(err.Error(), "threadId is required") {
		t.Fatalf("err=%q", err)
	}
}

func TestGetAutoContinueStateNotFound(t *testing.T) {
	deps, _ := newAutoContinueDeps()
	_, err := getAutoContinueState(context.Background(), deps, uiAutoContinueStateGetParams{
		Path: "_internal/auto-continue/state/thread_missing.json",
	})
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestAutoContinueStateRequiresStoresConfigured(t *testing.T) {
	emptyDeps := memoryHandlerDeps{}
	if _, err := getAutoContinueState(context.Background(), emptyDeps, uiAutoContinueStateGetParams{
		Path: "_internal/auto-continue/state/thread_abc.json",
	}); err == nil {
		t.Fatal("expected error when SharedFiles is nil")
	}
	if _, err := upsertAutoContinueState(context.Background(), emptyDeps, uiAutoContinueStateUpsertParams{
		Path:     "_internal/auto-continue/state/thread_abc.json",
		ThreadID: "thread_abc",
		Content:  validACSContent,
	}); err == nil {
		t.Fatal("expected error when SharedFilesUpserter is nil")
	}
	if _, err := deleteAutoContinueState(context.Background(), emptyDeps, uiAutoContinueStateDeleteParams{
		Path: "_internal/auto-continue/state/thread_abc.json",
	}); err == nil {
		t.Fatal("expected error when SharedFilesDeleter is nil")
	}
}
