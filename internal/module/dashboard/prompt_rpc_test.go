package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func TestPromptHandlersWriteListDelete(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	server := newPromptTestServer(store)

	createRes := dispatchPrompt[promptWriteResponse](t, server, "prompts/write", `{"name":"Main Prompt","content":"hello","description":"first","agentType":"main","cwd":"/repo"}`)
	if createRes.Prompt.ID == "" || createRes.Prompt.Name != "Main Prompt" {
		t.Fatalf("create response = %#v", createRes)
	}
	if got := promptScopeFromTags(store.items[createRes.Prompt.ID].Tags); got != "/repo" {
		t.Fatalf("stored scope = %q, want /repo", got)
	}

	updateRes := dispatchPrompt[promptWriteResponse](t, server, "prompts/write", `{"id":"`+createRes.Prompt.ID+`","name":"Main Prompt","content":"updated","description":"second","agentType":"sub","cwd":"/repo"}`)
	if updateRes.Prompt.Content != "updated" || updateRes.Prompt.AgentType != "sub" {
		t.Fatalf("update response = %#v", updateRes)
	}
	if len(store.versions) != 1 {
		t.Fatalf("versions after update = %d", len(store.versions))
	}

	listRes := dispatchPrompt[promptListResponse](t, server, "prompts/list", `{"cwd":"/repo"}`)
	if len(listRes.Prompts) != 1 || listRes.Prompts[0].Content != "updated" {
		t.Fatalf("list response = %#v", listRes)
	}

	deleteRes := dispatchPrompt[promptDeleteResponse](t, server, "prompts/delete", `{"id":"`+createRes.Prompt.ID+`","cwd":"/repo"}`)
	if !deleteRes.OK {
		t.Fatalf("delete response = %#v", deleteRes)
	}
	if len(store.versions) != 2 {
		t.Fatalf("versions after delete = %d", len(store.versions))
	}
	if store.versions[0].Description != "first" || store.versions[1].Description != "second" {
		t.Fatalf("archived descriptions = %#v", store.versions)
	}

	listRes = dispatchPrompt[promptListResponse](t, server, "prompts/list", `{}`)
	if len(listRes.Prompts) != 0 {
		t.Fatalf("prompts after delete = %#v", listRes.Prompts)
	}
}

func TestPromptListFiltersByCWDAndKeepsLegacyGlobal(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/global",
		Title:       "Global",
		AgentKey:    "main",
		PromptText:  "global",
		Description: "global",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/repo",
		Title:       "Repo",
		AgentKey:    "main",
		PromptText:  "repo",
		Description: "repo",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/repo"),
		Enabled:     true,
	})
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/other",
		Title:       "Other",
		AgentKey:    "main",
		PromptText:  "other",
		Description: "other",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/other"),
		Enabled:     true,
	})
	server := newPromptTestServer(store)

	listRes := dispatchPrompt[promptListResponse](t, server, "prompts/list", `{"cwd":"/repo"}`)
	if len(listRes.Prompts) != 2 {
		t.Fatalf("filtered prompts = %#v", listRes.Prompts)
	}
}

type promptWriteResponse struct {
	Prompt promptRPCItem `json:"prompt"`
}

type promptListResponse struct {
	Prompts []promptRPCItem `json:"prompts"`
}

type promptDeleteResponse struct {
	OK bool `json:"ok"`
}

func TestPromptWriteRejectsNilStore(t *testing.T) {
	t.Parallel()

	server := newPromptTestServer(nil)
	err := dispatchPromptErr(t, server, "prompts/write", `{"name":"Missing Store","content":"hello"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/write) error = nil, want missing store")
	}
}

func TestPromptWriteRejectsEmptyName(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	server := newPromptTestServer(store)
	err := dispatchPromptErr(t, server, "prompts/write", `{"name":"","content":"hello"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/write) error = nil, want name validation error")
	}
}

func TestPromptWriteRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	server := newPromptTestServer(store)
	err := dispatchPromptErr(t, server, "prompts/write", `{"name":"Big","content":"`+strings.Repeat("a", promptMaxContentBytes+1)+`"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/write oversized content) error = nil")
	}
}

func TestPromptWriteRejectsOversizedDescription(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	server := newPromptTestServer(store)
	err := dispatchPromptErr(t, server, "prompts/write", `{"name":"Big","content":"ok","description":"`+strings.Repeat("d", promptMaxDescriptionBytes+1)+`"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/write oversized description) error = nil")
	}
}

func TestPromptDeleteRejectsEmptyID(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	server := newPromptTestServer(store)
	err := dispatchPromptErr(t, server, "prompts/delete", `{"id":""}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/delete) error = nil, want id validation error")
	}
}

func TestPromptWriteRollsBackOnUpsertFailure(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	current, _ := store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/upsert-fail",
		Title:       "Before",
		AgentKey:    "main",
		PromptText:  "body",
		Description: "before",
		Variables:   json.RawMessage("{}"),
		Tags:        withPromptScopeTag(json.RawMessage("[]"), "/repo"),
		Enabled:     true,
	})
	store.failUpsert = errors.New("upsert failed")
	server := newPromptTestServer(store)

	err := dispatchPromptErr(t, server, "prompts/write", `{"id":"`+current.PromptKey+`","name":"After","content":"next","description":"after","cwd":"/repo"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/write) error = nil, want upsert failure")
	}
	if len(store.versions) != 0 {
		t.Fatalf("versions after upsert rollback = %d, want 0", len(store.versions))
	}
	got, getErr := store.Get(context.Background(), current.PromptKey)
	if getErr != nil {
		t.Fatalf("Get() error = %v", getErr)
	}
	if got.Title != "Before" || got.Description != "before" {
		t.Fatalf("prompt after rollback = %#v", got)
	}
}

func TestPromptDeleteReturnsArchiveFailure(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/archive-fail",
		Title:       "Archive Fail",
		AgentKey:    "main",
		PromptText:  "body",
		Description: "desc",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     true,
	})
	store.failInsertVersion = errors.New("archive failed")
	server := newPromptTestServer(store)

	err := dispatchPromptErr(t, server, "prompts/delete", `{"id":"main/archive-fail"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/delete) error = nil, want archive failure")
	}
	if len(store.versions) != 0 {
		t.Fatalf("versions after archive failure = %d, want 0", len(store.versions))
	}
	if _, getErr := store.Get(context.Background(), "main/archive-fail"); getErr != nil {
		t.Fatalf("Get() error = %v, want item preserved", getErr)
	}
}

func TestPromptDeleteRollsBackOnDeleteFailure(t *testing.T) {
	t.Parallel()

	store := newPromptStoreStub()
	_, _ = store.Upsert(context.Background(), promptstore.PromptTemplate{
		PromptKey:   "main/delete-fail",
		Title:       "Delete Fail",
		AgentKey:    "main",
		PromptText:  "body",
		Description: "desc",
		Variables:   json.RawMessage("{}"),
		Tags:        json.RawMessage("[]"),
		Enabled:     true,
	})
	store.failDelete = errors.New("delete failed")
	server := newPromptTestServer(store)

	err := dispatchPromptErr(t, server, "prompts/delete", `{"id":"main/delete-fail"}`)
	if err == nil {
		t.Fatal("Dispatch(prompts/delete) error = nil, want delete failure")
	}
	if len(store.versions) != 0 {
		t.Fatalf("versions after delete rollback = %d, want 0", len(store.versions))
	}
	if _, getErr := store.Get(context.Background(), "main/delete-fail"); getErr != nil {
		t.Fatalf("Get() error = %v, want item preserved", getErr)
	}
}

func newPromptTestServer(store promptstore.Store) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{
		Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"},
	})
	server.Register(buildPromptHandlers(store).Handlers)
	return server
}

func dispatchPrompt[T any](t *testing.T, server *platformrpc.Server, method, raw string) T {
	t.Helper()

	result, err := server.Dispatch(context.Background(), method, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Dispatch(%q) error = %v", method, err)
	}
	var value T
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", method, err)
	}
	return value
}

func dispatchPromptErr(t *testing.T, server *platformrpc.Server, method, raw string) error {
	t.Helper()

	_, err := server.Dispatch(context.Background(), method, json.RawMessage(raw))
	return err
}

type promptStoreStub struct {
	items             map[string]promptstore.PromptTemplate
	versions          []promptstore.PromptTemplateVersion
	nextID            int64
	now               time.Time
	failDelete        error
	failInsertVersion error
	failUpsert        error
}

func newPromptStoreStub() *promptStoreStub {
	return &promptStoreStub{
		items:  map[string]promptstore.PromptTemplate{},
		nextID: 1,
		now:    time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC),
	}
}

func (s *promptStoreStub) Get(_ context.Context, promptKey string) (*promptstore.PromptTemplate, error) {
	item, ok := s.items[promptKey]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	out := item
	return &out, nil
}

func (s *promptStoreStub) WithTx(_ context.Context, fn func(txStore promptstore.Store) error) error {
	if s == nil {
		return errPromptStoreRequired
	}
	txStore := s.clone()
	if err := fn(txStore); err != nil {
		return err
	}
	s.commitFrom(txStore)
	return nil
}

func (s *promptStoreStub) Delete(_ context.Context, promptKey string) error {
	if s.failDelete != nil {
		return s.failDelete
	}
	if _, ok := s.items[promptKey]; !ok {
		return platformdb.ErrNotFound
	}
	delete(s.items, promptKey)
	return nil
}

func (s *promptStoreStub) InsertVersion(_ context.Context, version promptstore.PromptTemplateVersion) error {
	if s.failInsertVersion != nil {
		return s.failInsertVersion
	}
	s.versions = append(s.versions, version)
	return nil
}

func (s *promptStoreStub) Upsert(_ context.Context, template promptstore.PromptTemplate) (*promptstore.PromptTemplate, error) {
	if s.failUpsert != nil {
		return nil, s.failUpsert
	}
	current, exists := s.items[template.PromptKey]
	if exists {
		template.ID = current.ID
		template.CreatedAt = current.CreatedAt
		s.now = s.now.Add(time.Second)
		template.UpdatedAt = s.now
	} else {
		template.ID = s.nextID
		s.nextID++
		s.now = s.now.Add(time.Second)
		template.CreatedAt = s.now
		template.UpdatedAt = s.now
	}
	s.items[template.PromptKey] = template
	out := template
	return &out, nil
}

func (s *promptStoreStub) List(_ context.Context, filter promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	items := make([]promptstore.PromptTemplate, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if filter.Limit > 0 && len(items) > int(filter.Limit) {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *promptStoreStub) clone() *promptStoreStub {
	cloned := &promptStoreStub{
		items:             make(map[string]promptstore.PromptTemplate, len(s.items)),
		versions:          append([]promptstore.PromptTemplateVersion(nil), s.versions...),
		nextID:            s.nextID,
		now:               s.now,
		failDelete:        s.failDelete,
		failInsertVersion: s.failInsertVersion,
		failUpsert:        s.failUpsert,
	}
	for key, item := range s.items {
		cloned.items[key] = item
	}
	return cloned
}

func (s *promptStoreStub) commitFrom(txStore *promptStoreStub) {
	s.items = txStore.items
	s.versions = txStore.versions
	s.nextID = txStore.nextID
	s.now = txStore.now
}
