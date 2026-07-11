package thread

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
)

func TestReadMessagesSupportsTimestampCursorCompatibility(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 21, 12, 0, 0, 0, time.UTC)
	session := &historyTestSession{
		threadID: "thread-1",
		messages: []dto.Message{
			{ID: 1, Role: "user", Content: "m1", Timestamp: base.Add(1 * time.Minute)},
			{ID: 2, Role: "assistant", Content: "m2", Timestamp: base.Add(2 * time.Minute)},
			{ID: 3, Role: "user", Content: "m3", Timestamp: base.Add(3 * time.Minute)},
			{ID: 4, Role: "assistant", Content: "m4", Timestamp: base.Add(4 * time.Minute)},
			{ID: 5, Role: "user", Content: "m5", Timestamp: base.Add(5 * time.Minute)},
			{ID: 6, Role: "assistant", Content: "m6", Timestamp: base.Add(6 * time.Minute)},
		},
	}
	svc := NewService(
		silentLogger(),
		nil,
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}),
		&historyTestSessionProvider{sessions: map[string]contract.Session{"agent-1": session}},
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.ReadMessages(context.Background(), "thread-1", 2, base.Add(5*time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("ReadMessages() error = %v", err)
	}
	want := dto.ThreadMessagesResult{
		Messages: []dto.Message{
			{ID: 4, AgentID: "agent-1", Role: "assistant", EventType: "agent_message", Content: "m4", Timestamp: base.Add(4 * time.Minute)},
			{ID: 3, AgentID: "agent-1", Role: "user", EventType: "", Content: "m3", Timestamp: base.Add(3 * time.Minute)},
		},
		Total: 2,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadMessages() = %#v, want %#v", got, want)
	}
	if got := session.readCalls; len(got) != 0 {
		t.Fatalf("read calls = %#v, want no full-history reads", got)
	}
	if got := session.pageCalls; len(got) != 1 {
		t.Fatalf("page calls = %#v, want 1 call", got)
	}
	for _, call := range session.pageCalls {
		if call.ThreadID != "thread-1" {
			t.Fatalf("page thread id = %q, want thread-1", call.ThreadID)
		}
	}
}

func TestReadMessagesFirstPageUsesMessagePageReader(t *testing.T) {
	t.Parallel()

	session := &historyTestSession{
		threadID: "thread-1",
		messages: []dto.Message{
			{Role: "user", Content: "m1"},
			{Role: "assistant", Content: "m2"},
			{Role: "user", Content: "m3"},
			{Role: "assistant", Content: "m4"},
		},
		page: dto.MessagePageResult{
			Messages: []dto.Message{
				{Role: "user", Content: "m3"},
				{Role: "assistant", Content: "m4"},
			},
			HasMore:    true,
			NextBefore: "opaque-before-m3",
		},
	}
	svc := NewService(
		silentLogger(),
		nil,
		newHistoryTestBindingStore(&BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
			CodexThreadID:    "thread-1",
		}),
		&historyTestSessionProvider{sessions: map[string]contract.Session{"agent-1": session}},
		nil,
		nil,
		nil,
		nil,
	)

	got, err := svc.ReadMessages(context.Background(), "thread-1", 2, "")
	if err != nil {
		t.Fatalf("ReadMessages() error = %v", err)
	}
	requireMessageContents(t, got.Messages, []string{"m4", "m3"})
	requireMessagesPageMetadata(t, got, true, "opaque-before-m3", 2)
	requireNoReadHistoryCalls(t, session)
	requireSinglePageCall(t, session, "thread-1", 2, "")
}

func requireMessageContents(t *testing.T, messages []dto.Message, want []string) {
	t.Helper()
	got := make([]string, 0, len(messages))
	for _, msg := range messages {
		got = append(got, msg.Content)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message contents = %#v, want %#v", got, want)
	}
}

func requireMessagesPageMetadata(t *testing.T, got dto.ThreadMessagesResult, hasMore bool, nextBefore string, total int64) {
	t.Helper()
	if got.HasMore != hasMore {
		t.Fatalf("hasMore = %v, want %v", got.HasMore, hasMore)
	}
	if got.NextBefore != nextBefore {
		t.Fatalf("nextBefore = %q, want %q", got.NextBefore, nextBefore)
	}
	if got.Total != total {
		t.Fatalf("total = %d, want %d", got.Total, total)
	}
}

func requireNoReadHistoryCalls(t *testing.T, session *historyTestSession) {
	t.Helper()
	if len(session.readCalls) != 0 {
		t.Fatalf("ReadHistory calls = %#v, want none", session.readCalls)
	}
}

func requireSinglePageCall(t *testing.T, session *historyTestSession, threadID string, limit int, before string) {
	t.Helper()
	if len(session.pageCalls) != 1 {
		t.Fatalf("ReadMessagesPage calls = %#v, want 1", session.pageCalls)
	}
	call := session.pageCalls[0]
	if call.ThreadID != threadID {
		t.Fatalf("ReadMessagesPage thread = %q, want %q", call.ThreadID, threadID)
	}
	if call.Request.Limit != limit {
		t.Fatalf("ReadMessagesPage limit = %d, want %d", call.Request.Limit, limit)
	}
	if call.Request.Before != before {
		t.Fatalf("ReadMessagesPage before = %q, want %q", call.Request.Before, before)
	}
}

func TestForkedThreadHistoryUsesForkThreadID(t *testing.T) {
	t.Parallel()

	fixture := newForkedThreadHistoryFixture(t)
	result, err := fixture.svc.Fork(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	requireForkedThreadResult(t, result, fixture.threadStore)
	requireForkedHistoryUsesForkThreadID(t, fixture)
}

type forkedThreadHistoryFixture struct {
	svc         Service
	parent      *historyTestSession
	forked      *historyTestSession
	threadStore *stubThreadStore
}

func newForkedThreadHistoryFixture(t *testing.T) forkedThreadHistoryFixture {
	t.Helper()
	parentSession := &historyTestSession{
		threadID:   "thread-1",
		forkResult: dto.ForkResult{NewThreadID: "thread-2"},
	}
	forkedSession := &historyTestSession{threadID: "thread-2"}
	threadStore := &stubThreadStore{
		thread: &ThreadRecord{
			ThreadID:       "thread-1",
			AgentID:        "agent-1",
			Prompt:         "demo",
			Model:          "gpt-5",
			Cwd:            "/tmp/demo",
			CreatedAt:      123,
			ConfigOverride: legacyPromptSnapshotMigrationConfig(t),
		},
	}
	bindings := newHistoryTestBindingStore(&BindingRecord{
		AgentID:          "agent-1",
		Provider:         "codex",
		ProviderThreadID: "thread-1",
		CodexThreadID:    "thread-1",
		Cwd:              "/tmp/demo",
	})
	sessions := &historyTestSessionProvider{sessions: map[string]contract.Session{
		"agent-1": parentSession,
	}}
	starter := &stubSessionStarter{
		onResume: func(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
			if req.AgentID != "thread-2" || req.ThreadID != "thread-2" {
				t.Fatalf("resume request = %#v, want independent thread-2 agent/session", req)
			}
			sessions.sessions["thread-2"] = forkedSession
			return forkedSession, nil
		},
	}
	svc := NewService(
		silentLogger(),
		threadStore,
		bindings,
		sessions,
		starter,
		nil,
		&forkOrchestrationStub{},
		nil,
	)
	return forkedThreadHistoryFixture{
		svc:         svc,
		parent:      parentSession,
		forked:      forkedSession,
		threadStore: threadStore,
	}
}

func requireForkedThreadResult(t *testing.T, result ForkResult, threadStore *stubThreadStore) {
	t.Helper()
	if result.NewThreadID != "thread-2" {
		t.Fatalf("Fork() new thread id = %q, want thread-2", result.NewThreadID)
	}
	if threadStore.upsertCount < 1 || threadStore.upsert.OwnerThreadID != "thread-1" {
		t.Fatalf("fork upsert = %#v (count %d), want owner_thread_id thread-1", threadStore.upsert, threadStore.upsertCount)
	}
}

func requireForkedHistoryUsesForkThreadID(t *testing.T, fixture forkedThreadHistoryFixture) {
	t.Helper()
	if _, err := fixture.svc.ReadHistory(context.Background(), "thread-2", 5); err != nil {
		t.Fatalf("ReadHistory(fork) error = %v", err)
	}
	if len(fixture.forked.readCalls) == 0 {
		t.Fatal("expected forked thread history read to hit session")
	}
	if got := fixture.forked.readCalls[len(fixture.forked.readCalls)-1].ThreadID; got != "thread-2" {
		t.Fatalf("forked thread history target = %q, want thread-2", got)
	}
	if len(fixture.parent.readCalls) != 0 {
		t.Fatalf("parent session read calls = %#v, want no reads after forked history lookup", fixture.parent.readCalls)
	}
}

type historyTestBindingStore struct {
	historyTestBindingNoopStore

	bindings map[string]BindingRecord
}

func newHistoryTestBindingStore(binding *BindingRecord) *historyTestBindingStore {
	store := &historyTestBindingStore{bindings: map[string]BindingRecord{}}
	if binding != nil {
		store.bindings[strings.TrimSpace(binding.AgentID)] = *binding
	}
	return store
}

func (s *historyTestBindingStore) GetByProviderThread(_ context.Context, provider, providerThreadID string) (*BindingRecord, error) {
	for _, binding := range s.bindings {
		if strings.TrimSpace(binding.Provider) != strings.TrimSpace(provider) || strings.TrimSpace(binding.ProviderThreadID) != strings.TrimSpace(providerThreadID) {
			continue
		}
		copy := binding
		return &copy, nil
	}
	return nil, platformdb.ErrNotFound
}

func (s *historyTestBindingStore) Upsert(_ context.Context, params BindingUpsert) error {
	if s.bindings == nil {
		s.bindings = map[string]BindingRecord{}
	}
	s.bindings[strings.TrimSpace(params.AgentID)] = BindingRecord{
		AgentID:          params.AgentID,
		Provider:         params.Provider,
		ProviderThreadID: params.ProviderThreadID,
		CodexThreadID:    params.CodexThreadID,
		RolloutPath:      params.RolloutPath,
		Cwd:              params.Cwd,
		CreatedAt:        params.CreatedAt,
		UpdatedAt:        params.UpdatedAt,
	}
	return nil
}

type historyTestBindingNoopStore struct{}

func (historyTestBindingNoopStore) DeleteByAgentID(context.Context, string) error { return nil }

func (historyTestBindingNoopStore) UpdateSessionUUID(context.Context, BindingSessionUUIDUpdate) error {
	return nil
}
func (historyTestBindingNoopStore) UpdateProviderThreadID(context.Context, BindingProviderThreadIDUpdate) error {
	return nil
}

func (historyTestBindingNoopStore) SetArchived(context.Context, BindingArchiveUpdate) error {
	return nil
}

func (s *historyTestBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	binding, ok := s.bindings[strings.TrimSpace(agentID)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	copy := binding
	return &copy, nil
}

func (historyTestBindingNoopStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}

func (historyTestBindingNoopStore) UnbindAgentThread(context.Context, string) error { return nil }

func (s *historyTestBindingStore) ListAgentThreadBindings(context.Context) ([]BindingRecord, error) {
	if len(s.bindings) == 0 {
		return nil, nil
	}
	out := make([]BindingRecord, 0, len(s.bindings))
	for _, binding := range s.bindings {
		out = append(out, binding)
	}
	return out, nil
}

func (s *historyTestBindingStore) GetThreadByAgent(_ context.Context, agentID string) (string, error) {
	binding, ok := s.bindings[strings.TrimSpace(agentID)]
	if !ok {
		return "", platformdb.ErrNotFound
	}
	return shared.FirstNonEmpty(binding.CodexThreadID, binding.ProviderThreadID), nil
}

func (historyTestBindingNoopStore) UpdateAgentCwd(context.Context, BindingCWDUpdate) error {
	return nil
}

func (historyTestBindingNoopStore) Rebind(context.Context, bindingstore.RebindParams) error {
	return nil
}

func (historyTestBindingNoopStore) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (historyTestBindingNoopStore) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

type historyTestSessionProvider struct {
	sessions map[string]contract.Session
}

func (p *historyTestSessionProvider) GetSession(agentID string) (contract.Session, error) {
	if p.sessions == nil {
		return nil, contract.ErrSessionNotFound
	}
	if session, ok := p.sessions[strings.TrimSpace(agentID)]; ok {
		return session, nil
	}
	return nil, contract.ErrSessionNotFound
}

func (p *historyTestSessionProvider) RemoveSession(sessionID string) {
	_ = p
	_ = sessionID
}

func (p *historyTestSessionProvider) SessionGeneration(string) uint64 {
	return 1
}

type historyReadCall struct {
	ThreadID string
	Limit    int
}

type historyPageCall struct {
	ThreadID string
	Request  dto.MessagePageRequest
}

type historyTestSession struct {
	historyTestSessionUnusedMethods

	threadID   string
	threads    []dto.ThreadRef
	messages   []dto.Message
	page       dto.MessagePageResult
	forkResult dto.ForkResult
	readCalls  []historyReadCall
	pageCalls  []historyPageCall
}

func (s *historyTestSession) ThreadID() string { return s.threadID }

type historyTestSessionUnusedMethods struct{}

func (historyTestSessionUnusedMethods) RolloutPath() string { return "" }

func (historyTestSessionUnusedMethods) Capabilities() dto.CapabilitySet { return nil }

func (historyTestSessionUnusedMethods) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}

func (historyTestSessionUnusedMethods) Interrupt(context.Context, dto.InterruptRequest) error {
	return nil
}

func (historyTestSessionUnusedMethods) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}

func (s *historyTestSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	if len(s.threads) == 0 {
		return nil, nil
	}
	return append([]dto.ThreadRef(nil), s.threads...), nil
}

func (s *historyTestSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return s.forkResult, nil
}

func (s *historyTestSession) ReadHistory(_ context.Context, threadID string, limit int) ([]dto.Message, error) {
	s.readCalls = append(s.readCalls, historyReadCall{
		ThreadID: strings.TrimSpace(threadID),
		Limit:    limit,
	})
	if limit <= 0 || limit >= len(s.messages) {
		return append([]dto.Message(nil), s.messages...), nil
	}
	start := len(s.messages) - limit
	return append([]dto.Message(nil), s.messages[start:]...), nil
}

func (s *historyTestSession) ReadMessagesPage(_ context.Context, threadID string, req dto.MessagePageRequest) (dto.MessagePageResult, error) {
	s.pageCalls = append(s.pageCalls, historyPageCall{
		ThreadID: strings.TrimSpace(threadID),
		Request:  req,
	})
	if hasHistoryTestPage(s.page) {
		return cloneHistoryTestPage(s.page), nil
	}
	messages, err := historyTestMessagesBefore(s.messages, req.Before)
	if err != nil {
		return dto.MessagePageResult{}, err
	}
	if req.Limit > 0 && len(messages) > req.Limit {
		messages = messages[len(messages)-req.Limit:]
	}
	return dto.MessagePageResult{Messages: messages}, nil
}

func hasHistoryTestPage(page dto.MessagePageResult) bool {
	return len(page.Messages) != 0 || page.HasMore || page.NextBefore != ""
}

func cloneHistoryTestPage(page dto.MessagePageResult) dto.MessagePageResult {
	page.Messages = append([]dto.Message(nil), page.Messages...)
	return page
}

func historyTestMessagesBefore(messages []dto.Message, before string) ([]dto.Message, error) {
	out := append([]dto.Message(nil), messages...)
	if before == "" {
		return out, nil
	}
	cutoff, err := parseBeforeCursor(before)
	if err != nil {
		return nil, err
	}
	out = paginateMessagesBeforeTime(out, len(out), cutoff)
	reverseHistoryTestMessages(out)
	return out, nil
}

func reverseHistoryTestMessages(messages []dto.Message) {
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
}

func (historyTestSessionUnusedMethods) Configure(context.Context, dto.ThreadConfigPatch) error {
	return nil
}

func (historyTestSessionUnusedMethods) Close(context.Context) error { return nil }

func (historyTestSessionUnusedMethods) ForceStop() error { return nil }
