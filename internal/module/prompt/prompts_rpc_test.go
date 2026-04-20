package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// ============================================================================
// p20.1 — prompts/list|write|delete 宿主 RPC 回归测试
// ============================================================================
//
// 目标：
//   1. NewPromptHandlers 把三个 RPC 方法都注册上去（消除 SystemPromptPage 404）
//   2. list / write / delete 的核心业务逻辑：scope 过滤 / scope 拦截 / archive 必走
//   3. nil store / 非法 payload 不 panic

// fakePromptStore 是 promptstore.Store 的内存实现，支持非 tx 语义（WithTx 直接执行 fn）。
type fakePromptStore struct {
	listFn      func(ctx context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error)
	getFn       func(ctx context.Context, key string) (*promptstore.PromptTemplate, error)
	deleteFn    func(ctx context.Context, key string) error
	upsertFn    func(ctx context.Context, t promptstore.PromptTemplate) (*promptstore.PromptTemplate, error)
	insertVerFn func(ctx context.Context, v promptstore.PromptTemplateVersion) error

	insertedVersions []promptstore.PromptTemplateVersion
	upsertedKeys     []string
	deletedKeys      []string
}

func (f *fakePromptStore) List(ctx context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	if f.listFn != nil {
		return f.listFn(ctx, filter)
	}
	return nil, nil
}
func (f *fakePromptStore) Get(ctx context.Context, key string) (*promptstore.PromptTemplate, error) {
	if f.getFn != nil {
		return f.getFn(ctx, key)
	}
	return nil, platformdb.ErrNotFound
}
func (f *fakePromptStore) Delete(ctx context.Context, key string) error {
	f.deletedKeys = append(f.deletedKeys, key)
	if f.deleteFn != nil {
		return f.deleteFn(ctx, key)
	}
	return nil
}
func (f *fakePromptStore) Upsert(ctx context.Context, t promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	f.upsertedKeys = append(f.upsertedKeys, t.PromptKey)
	if f.upsertFn != nil {
		return f.upsertFn(ctx, t)
	}
	return &t, nil
}
func (f *fakePromptStore) InsertVersion(ctx context.Context, v promptstore.PromptTemplateVersion) error {
	f.insertedVersions = append(f.insertedVersions, v)
	if f.insertVerFn != nil {
		return f.insertVerFn(ctx, v)
	}
	return nil
}
func (f *fakePromptStore) WithTx(ctx context.Context, fn func(promptstore.Store) error) error {
	return fn(f)
}

// ---------------------------------------------------------------------------
// Handler registration
// ---------------------------------------------------------------------------

func TestNewPromptHandlersExposeLegacyPromptsMethods(t *testing.T) {
	got := NewPromptHandlers(&fakePromptStore{})
	for _, method := range []string{"prompts/list", "prompts/write", "prompts/delete"} {
		if _, ok := got.Handlers[method]; !ok {
			t.Fatalf("method %q not registered", method)
		}
	}
}

// ---------------------------------------------------------------------------
// ListPrompts: filter enabled + scope
// ---------------------------------------------------------------------------

func TestListPromptsFiltersDisabledAndScope(t *testing.T) {
	store := &fakePromptStore{
		listFn: func(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
			return []promptstore.PromptTemplate{
				{PromptKey: "a", Enabled: true, Tags: json.RawMessage(`["scope.cwd:/repoA"]`)},
				{PromptKey: "b", Enabled: false, Tags: json.RawMessage(`["scope.cwd:/repoA"]`)},
				{PromptKey: "c", Enabled: true, Tags: json.RawMessage(`["scope.cwd:/repoB"]`)},
				{PromptKey: "d", Enabled: true, Tags: json.RawMessage(`[]`)},
			}, nil
		},
	}
	svc := newPromptRPCService(store)
	got, err := svc.ListPrompts(context.Background(), "/repoA", "")
	if err != nil {
		t.Fatalf("ListPrompts err: %v", err)
	}
	keys := make([]string, 0, len(got))
	for _, t := range got {
		keys = append(keys, t.PromptKey)
	}
	// a 命中 scope；b disabled；c 不同 scope；d 无 scope tag → 可见
	if len(keys) != 2 || keys[0] != "a" || keys[1] != "d" {
		t.Fatalf("want [a d], got %v", keys)
	}
}

// ---------------------------------------------------------------------------
// WritePrompt: archive before upsert + cwd scope stamp
// ---------------------------------------------------------------------------

func TestWritePromptArchivesThenUpsertsWithScopeTag(t *testing.T) {
	now := time.Now()
	existing := &promptstore.PromptTemplate{
		PromptKey:  "main/foo",
		Title:      "Foo",
		AgentKey:   "main",
		PromptText: "old body",
		Tags:       json.RawMessage(`["scope.cwd:/repoA"]`),
		UpdatedAt:  now,
	}
	store := &fakePromptStore{
		getFn: func(_ context.Context, k string) (*promptstore.PromptTemplate, error) {
			if k == "main/foo" {
				return existing, nil
			}
			return nil, platformdb.ErrNotFound
		},
	}
	svc := newPromptRPCService(store)
	got, err := svc.WritePrompt(context.Background(), "/repoA", PromptWriteRequest{
		ID: "main/foo", Name: "Foo", Content: "new body", AgentType: "main",
	})
	if err != nil {
		t.Fatalf("WritePrompt err: %v", err)
	}
	if got == nil || got.PromptKey != "main/foo" {
		t.Fatalf("unexpected result: %+v", got)
	}
	// Archive 先调，Upsert 后调
	if len(store.insertedVersions) != 1 {
		t.Fatalf("archive must run once, got %d", len(store.insertedVersions))
	}
	if len(store.upsertedKeys) != 1 || store.upsertedKeys[0] != "main/foo" {
		t.Fatalf("upsert must run once with main/foo, got %v", store.upsertedKeys)
	}
}

func TestWritePromptRejectsCrossScope(t *testing.T) {
	store := &fakePromptStore{
		getFn: func(_ context.Context, _ string) (*promptstore.PromptTemplate, error) {
			return &promptstore.PromptTemplate{
				PromptKey: "main/foo",
				Tags:      json.RawMessage(`["scope.cwd:/repoA"]`),
			}, nil
		},
	}
	svc := newPromptRPCService(store)
	_, err := svc.WritePrompt(context.Background(), "/repoB", PromptWriteRequest{
		ID: "main/foo", Name: "Foo", Content: "body",
	})
	if err == nil {
		t.Fatal("cross-scope write should error")
	}
}

// ---------------------------------------------------------------------------
// DeletePrompt: archive → delete + scope gate
// ---------------------------------------------------------------------------

func TestDeletePromptArchivesBeforeDelete(t *testing.T) {
	now := time.Now()
	store := &fakePromptStore{
		getFn: func(_ context.Context, _ string) (*promptstore.PromptTemplate, error) {
			return &promptstore.PromptTemplate{
				PromptKey: "main/foo",
				Tags:      json.RawMessage(`["scope.cwd:/repoA"]`),
				UpdatedAt: now,
			}, nil
		},
	}
	svc := newPromptRPCService(store)
	if err := svc.DeletePrompt(context.Background(), "/repoA", "main/foo"); err != nil {
		t.Fatalf("DeletePrompt err: %v", err)
	}
	if len(store.insertedVersions) != 1 {
		t.Fatal("delete must archive before removing")
	}
	if len(store.deletedKeys) != 1 || store.deletedKeys[0] != "main/foo" {
		t.Fatalf("delete must remove once with key, got %v", store.deletedKeys)
	}
}

func TestDeletePromptRejectsCrossScope(t *testing.T) {
	store := &fakePromptStore{
		getFn: func(_ context.Context, _ string) (*promptstore.PromptTemplate, error) {
			return &promptstore.PromptTemplate{
				PromptKey: "main/foo",
				Tags:      json.RawMessage(`["scope.cwd:/repoA"]`),
			}, nil
		},
	}
	svc := newPromptRPCService(store)
	err := svc.DeletePrompt(context.Background(), "/repoB", "main/foo")
	if err == nil {
		t.Fatal("cross-scope delete should error")
	}
}

// ---------------------------------------------------------------------------
// nil store safety
// ---------------------------------------------------------------------------

func TestPromptsServiceNilStore(t *testing.T) {
	svc := newPromptRPCService(nil)
	if list, err := svc.ListPrompts(context.Background(), "", ""); err != nil || len(list) != 0 {
		t.Fatalf("nil store list: want empty + nil err, got %v %v", list, err)
	}
	if _, err := svc.WritePrompt(context.Background(), "", PromptWriteRequest{Name: "x"}); !errors.Is(err, errPromptStoreRequired) {
		t.Fatalf("nil store write: want errPromptStoreRequired, got %v", err)
	}
	if err := svc.DeletePrompt(context.Background(), "", "k"); !errors.Is(err, errPromptStoreRequired) {
		t.Fatalf("nil store delete: want errPromptStoreRequired, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// validation / key slug
// ---------------------------------------------------------------------------

func TestValidatePromptWriteRejectsEmptyName(t *testing.T) {
	if err := validatePromptWrite(PromptWriteRequest{Name: "  "}); err == nil {
		t.Fatal("empty name should fail")
	}
}

func TestPromptKeyBaseSlugify(t *testing.T) {
	if got := promptKeyBase("main", "Hello World 你好"); got != "main/hello-world" {
		t.Fatalf("slugify: got %q", got)
	}
	if got := promptKeyBase("", "&&&"); got != "main/prompt" {
		t.Fatalf("fallback slug: got %q", got)
	}
}
