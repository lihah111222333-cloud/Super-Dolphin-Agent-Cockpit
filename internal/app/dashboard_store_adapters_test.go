package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/module/dashboard"
	agentstatusstore "github.com/anthropic-ai/super-agent-v3/internal/store/agentstatus"
	ailogstore "github.com/anthropic-ai/super-agent-v3/internal/store/ailog"
	auditlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/auditlog"
	buslogstore "github.com/anthropic-ai/super-agent-v3/internal/store/buslog"
	commandcardstore "github.com/anthropic-ai/super-agent-v3/internal/store/commandcard"
	dbquerystore "github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	systemlogstore "github.com/anthropic-ai/super-agent-v3/internal/store/systemlog"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

type dashboardAgentStatusRoot struct {
	agentstatusstore.Store
	err error
}
type dashboardAILogRoot struct {
	ailogstore.Store
	err error
}
type dashboardAuditLogRoot struct {
	auditlogstore.Store
	err error
}
type dashboardBusLogRoot struct {
	buslogstore.Store
	err error
}
type dashboardSystemLogRoot struct {
	systemlogstore.Store
	err error
}
type dashboardCommandCardRoot struct {
	commandcardstore.Reader
	err error
}
type dashboardPromptRoot struct {
	promptstore.Reader
	err error
}

func (s *dashboardAgentStatusRoot) List(context.Context, string) ([]agentstatusstore.AgentStatus, error) {
	return nil, s.err
}
func (s *dashboardAILogRoot) List(context.Context, ailogstore.ListFilter) ([]ailogstore.AILog, error) {
	return nil, s.err
}
func (s *dashboardAILogRoot) ListByCategory(context.Context, string, string, int32) ([]ailogstore.AILog, error) {
	return nil, s.err
}
func (s *dashboardAILogRoot) CountByStatus(context.Context) ([]ailogstore.StatusCount, error) {
	return nil, s.err
}
func (s *dashboardAILogRoot) ListRecent(context.Context, int32) ([]ailogstore.AILog, error) {
	return nil, s.err
}
func (s *dashboardAuditLogRoot) List(context.Context, auditlogstore.ListFilter) ([]auditlogstore.AuditEvent, error) {
	return nil, s.err
}
func (s *dashboardBusLogRoot) List(context.Context, buslogstore.ListFilter) ([]buslogstore.BusExceptionLog, error) {
	return nil, s.err
}
func (s *dashboardBusLogRoot) Get(context.Context, int64) (buslogstore.BusExceptionLog, error) {
	return buslogstore.BusExceptionLog{}, s.err
}
func (s *dashboardSystemLogRoot) List(context.Context, systemlogstore.ListFilter) ([]systemlogstore.SystemLog, error) {
	return nil, s.err
}
func (s *dashboardCommandCardRoot) List(context.Context, commandcardstore.ListFilter) ([]commandcardstore.CommandCard, error) {
	return nil, s.err
}
func (s *dashboardPromptRoot) List(context.Context, promptstore.ListFilter) ([]promptstore.PromptTemplate, error) {
	return nil, s.err
}

type dashboardDBQueryRoot struct {
	dbquerystore.Store
	args []any
	rows []map[string]any
	err  error
}

func (s *dashboardDBQueryRoot) Query(_ context.Context, _ string, args ...any) ([]map[string]any, error) {
	s.args = args
	return s.rows, s.err
}

type dashboardSharedFileReaderTestDouble struct {
	file *sharedfilestore.SharedFile
	err  error
}

func (s *dashboardSharedFileReaderTestDouble) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	return s.file, s.err
}
func (s *dashboardSharedFileReaderTestDouble) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, s.err
}

type dashboardSharedFileStoreTestDouble struct {
	dashboardSharedFileReaderTestDouble
	upserted sharedfilestore.UpsertParams
}

func (s *dashboardSharedFileStoreTestDouble) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	s.upserted = params
	return s.file, s.err
}

// TestDashboardStoreAdapterProvidersExist 固定九个 provider 由 App 组合边界拥有。
func TestDashboardStoreAdapterProvidersExist(t *testing.T) {
	t.Parallel()
	ports := []any{
		provideDashboardAgentStatusReader(nil), provideDashboardAILogReader(nil),
		provideDashboardAuditLogReader(nil), provideDashboardBusLogReader(nil),
		provideDashboardSystemLogReader(nil), provideDashboardDBQueryExecutor(nil),
		provideDashboardCommandCardReader(nil), provideDashboardPromptTemplateReader(nil),
		provideDashboardSharedFileReader(nil),
	}
	for index, port := range ports {
		if port != nil {
			t.Fatalf("provider %d = %T, want nil", index, port)
		}
	}
}

// TestDashboardStoreAdapterProvidersPreserveTypedNil 固定九个 provider 对 typed nil 直接返回 nil。
func TestDashboardStoreAdapterProvidersPreserveTypedNil(t *testing.T) {
	t.Parallel()
	var agent *dashboardAgentStatusRoot
	var ai *dashboardAILogRoot
	var audit *dashboardAuditLogRoot
	var bus *dashboardBusLogRoot
	var system *dashboardSystemLogRoot
	var query *dashboardDBQueryRoot
	var command *dashboardCommandCardRoot
	var prompt *dashboardPromptRoot
	var shared *dashboardSharedFileReaderTestDouble
	ports := []any{provideDashboardAgentStatusReader(agent), provideDashboardAILogReader(ai), provideDashboardAuditLogReader(audit), provideDashboardBusLogReader(bus), provideDashboardSystemLogReader(system), provideDashboardDBQueryExecutor(query), provideDashboardCommandCardReader(command), provideDashboardPromptTemplateReader(prompt), provideDashboardSharedFileReader(shared)}
	for index, port := range ports {
		if port != nil {
			t.Fatalf("typed nil provider %d = %T, want nil", index, port)
		}
	}
}

// TestDashboardSharedFileAdapterPreservesWriterCapability 固定读写 concrete Store 的动态写能力。
func TestDashboardSharedFileAdapterPreservesWriterCapability(t *testing.T) {
	t.Parallel()
	port := provideDashboardSharedFileReader(&dashboardSharedFileStoreTestDouble{})
	if _, ok := port.(dashboard.SharedFileWriter); !ok {
		t.Fatalf("adapter = %T, want SharedFileWriter", port)
	}
}

// TestDashboardSharedFileAdapterOmitsWriterWithoutUpserter 固定 reader-only 不虚构写能力。
func TestDashboardSharedFileAdapterOmitsWriterWithoutUpserter(t *testing.T) {
	t.Parallel()
	port := provideDashboardSharedFileReader(&dashboardSharedFileReaderTestDouble{})
	if _, ok := port.(dashboard.SharedFileWriter); ok {
		t.Fatalf("reader-only adapter = %T, unexpectedly implements SharedFileWriter", port)
	}
}

// TestDashboardWriteWorkflowMaterialUsesAppAdapter 验证服务通过真实 App adapter 写入材料并保留错误身份。
func TestDashboardWriteWorkflowMaterialUsesAppAdapter(t *testing.T) {
	stored := &sharedfilestore.SharedFile{Path: "reports/workflows/uploads/spec.md", Content: "body", UpdatedBy: "dashboard-ui"}
	root := &dashboardSharedFileStoreTestDouble{dashboardSharedFileReaderTestDouble: dashboardSharedFileReaderTestDouble{file: stored}}
	port := provideDashboardSharedFileReader(root)
	svc := dashboard.NewService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, port, nil)
	got, err := svc.WriteWorkflowMaterial(context.Background(), dashboard.WorkflowMaterialWriteRequest{Path: stored.Path, Content: stored.Content})
	if err != nil || got == nil || got.Path != stored.Path || got.Content != stored.Content {
		t.Fatalf("WriteWorkflowMaterial = (%#v, %v)", got, err)
	}
	if root.upserted.Path != stored.Path || root.upserted.Content != stored.Content || root.upserted.UpdatedBy != "dashboard-ui" {
		t.Fatalf("Upsert params = %#v", root.upserted)
	}
	wantErr := errors.New("shared file write failed")
	root.err = wantErr
	if _, err := svc.WriteWorkflowMaterial(context.Background(), dashboard.WorkflowMaterialWriteRequest{Path: stored.Path, Content: stored.Content}); err != wantErr || !errors.Is(err, wantErr) {
		t.Fatalf("write error = %v, want identical %v", err, wantErr)
	}
}

// TestDashboardDBQueryAdapterCopiesArgumentsAndRows 固定查询输入和递归结果不共享 Store 所有权。
func TestDashboardDBQueryAdapterCopiesArgumentsAndRows(t *testing.T) {
	raw := json.RawMessage(`{"ok":true}`)
	bytes := []byte("bytes")
	nested := map[string]any{"items": []any{raw, bytes}}
	root := &dashboardDBQueryRoot{rows: []map[string]any{{"nested": nested}}}
	port := provideDashboardDBQueryExecutor(root)
	args := []any{"original"}
	rows, err := port.Query(context.Background(), "select 1", args...)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	root.args[0] = "changed"
	resultItems := rows[0]["nested"].(map[string]any)["items"].([]any)
	resultItems[0].(json.RawMessage)[0] = '['
	resultItems[1].([]byte)[0] = 'B'
	rows[0]["new"] = true
	if args[0] != "original" || string(raw) != `{"ok":true}` || string(bytes) != "bytes" || root.rows[0]["new"] != nil {
		t.Fatalf("query ownership leaked: args=%v raw=%s bytes=%s rows=%v", args, raw, bytes, root.rows)
	}
	wantErr := errors.New("query failed")
	root.err = wantErr
	if _, err := port.Query(context.Background(), "select 1"); err != wantErr || !errors.Is(err, wantErr) {
		t.Fatalf("query error = %v, want identical %v", err, wantErr)
	}
}

// TestDashboardStoreAdapterFieldCoverage one-hot 覆盖全部 Store/domain DTO 与输入映射。
func TestDashboardStoreAdapterFieldCoverage(t *testing.T) {
	storeadaptertest.AssertFieldsMapE(t, func(v agentstatusstore.AgentStatus) (dashboard.AgentStatus, error) {
		return mapDashboardAgentStatuses([]agentstatusstore.AgentStatus{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v ailogstore.AILog) (dashboard.AILog, error) {
		return mapDashboardAILogs([]ailogstore.AILog{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v ailogstore.StatusCount) (dashboard.AILogStatusCount, error) {
		return mapDashboardAILogStatusCounts([]ailogstore.StatusCount{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v auditlogstore.AuditEvent) (dashboard.AuditEvent, error) {
		return mapDashboardAuditEvents([]auditlogstore.AuditEvent{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v buslogstore.BusExceptionLog) (dashboard.BusExceptionLog, error) {
		return mapDashboardBusLog(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v systemlogstore.SystemLog) (dashboard.SystemLog, error) {
		return mapDashboardSystemLogs([]systemlogstore.SystemLog{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v commandcardstore.CommandCard) (dashboard.CommandCard, error) {
		return mapDashboardCommandCards([]commandcardstore.CommandCard{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v promptstore.PromptTemplate) (dashboard.PromptTemplate, error) {
		return mapDashboardPromptTemplates([]promptstore.PromptTemplate{v})[0], nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v sharedfilestore.SharedFile) (dashboard.SharedFile, error) {
		return mapDashboardSharedFile(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.AILogFilter) (ailogstore.ListFilter, error) {
		return toStoreDashboardAILogFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.AuditLogFilter) (auditlogstore.ListFilter, error) {
		return toStoreDashboardAuditLogFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.BusLogFilter) (buslogstore.ListFilter, error) {
		return toStoreDashboardBusLogFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.SystemLogFilter) (systemlogstore.ListFilter, error) {
		return toStoreDashboardSystemLogFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.CommandCardFilter) (commandcardstore.ListFilter, error) {
		return toStoreDashboardCommandCardFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.PromptTemplateFilter) (promptstore.ListFilter, error) {
		return toStoreDashboardPromptTemplateFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.SharedFileFilter) (sharedfilestore.ListFilter, error) {
		return toStoreDashboardSharedFileFilter(v), nil
	})
	storeadaptertest.AssertFieldsMapE(t, func(v dashboard.SharedFileUpsertParams) (sharedfilestore.UpsertParams, error) {
		return toStoreDashboardSharedFileUpsert(v), nil
	})
}

// TestDashboardStoreAdaptersCopyMutableFields 固定所有 JSON 与标量指针跨边界独立。
func TestDashboardStoreAdaptersCopyMutableFields(t *testing.T) {
	raw := json.RawMessage(`{}`)
	duration := int32(1)
	now := time.Now()
	agent := agentstatusstore.AgentStatus{OutputTail: raw}
	mappedAgent := mapDashboardAgentStatuses([]agentstatusstore.AgentStatus{agent})[0]
	mappedAgent.OutputTail[0] = '['
	ai := ailogstore.AILog{DurationMs: &duration, Extra: raw}
	mappedAI := mapDashboardAILogs([]ailogstore.AILog{ai})[0]
	*mappedAI.DurationMs = 2
	mappedAI.Extra[0] = '['
	audit := auditlogstore.AuditEvent{Extra: raw}
	mapDashboardAuditEvents([]auditlogstore.AuditEvent{audit})[0].Extra[0] = '['
	bus := buslogstore.BusExceptionLog{Extra: raw}
	mapDashboardBusLog(bus).Extra[0] = '['
	system := systemlogstore.SystemLog{DurationMs: &duration, Extra: raw}
	mappedSystem := mapDashboardSystemLogs([]systemlogstore.SystemLog{system})[0]
	*mappedSystem.DurationMs = 3
	mappedSystem.Extra[0] = '['
	card := commandcardstore.CommandCard{ArgsSchema: raw, LastRunAt: &now}
	mappedCard := mapDashboardCommandCards([]commandcardstore.CommandCard{card})[0]
	mappedCard.ArgsSchema[0] = '['
	*mappedCard.LastRunAt = now.Add(time.Hour)
	prompt := promptstore.PromptTemplate{Variables: raw, Tags: raw, MatchWhen: raw}
	mappedPrompt := mapDashboardPromptTemplates([]promptstore.PromptTemplate{prompt})[0]
	mappedPrompt.Variables[0] = '['
	mappedPrompt.Tags[0] = '['
	mappedPrompt.MatchWhen[0] = '['
	if string(raw) != "{}" || duration != 1 || !card.LastRunAt.Equal(now) {
		t.Fatal("mutable Store fields shared with dashboard DTO")
	}
}

// TestDashboardStoreAdaptersReturnIndependentLists 固定各列表 mapper 分配新切片。
func TestDashboardStoreAdaptersReturnIndependentLists(t *testing.T) {
	agents := []agentstatusstore.AgentStatus{{AgentID: "a"}}
	mapDashboardAgentStatuses(agents)[0].AgentID = "x"
	ai := []ailogstore.AILog{{ID: 1}}
	mapDashboardAILogs(ai)[0].ID = 2
	audit := []auditlogstore.AuditEvent{{ID: 1}}
	mapDashboardAuditEvents(audit)[0].ID = 2
	bus := []buslogstore.BusExceptionLog{{ID: 1}}
	mapDashboardBusLogs(bus)[0].ID = 2
	system := []systemlogstore.SystemLog{{ID: 1}}
	mapDashboardSystemLogs(system)[0].ID = 2
	cards := []commandcardstore.CommandCard{{ID: 1}}
	mapDashboardCommandCards(cards)[0].ID = 2
	prompts := []promptstore.PromptTemplate{{ID: 1}}
	mapDashboardPromptTemplates(prompts)[0].ID = 2
	files := []sharedfilestore.SharedFile{{Path: "a"}}
	mapDashboardSharedFiles(files)[0].Path = "x"
	counts := []ailogstore.StatusCount{{Status: "ok"}}
	mapDashboardAILogStatusCounts(counts)[0].Status = "x"
	if agents[0].AgentID != "a" || ai[0].ID != 1 || audit[0].ID != 1 || bus[0].ID != 1 || system[0].ID != 1 || cards[0].ID != 1 || prompts[0].ID != 1 || files[0].Path != "a" || counts[0].Status != "ok" {
		t.Fatal("mapper returned shared list")
	}
}

// TestDashboardStoreAdaptersPreserveErrors 覆盖九个端口的全部方法族错误身份。
func TestDashboardStoreAdaptersPreserveErrors(t *testing.T) {
	want := errors.New("dashboard Store failed")
	ctx := context.Background()
	agent := provideDashboardAgentStatusReader(&dashboardAgentStatusRoot{err: want})
	ai := provideDashboardAILogReader(&dashboardAILogRoot{err: want})
	audit := provideDashboardAuditLogReader(&dashboardAuditLogRoot{err: want})
	bus := provideDashboardBusLogReader(&dashboardBusLogRoot{err: want})
	system := provideDashboardSystemLogReader(&dashboardSystemLogRoot{err: want})
	command := provideDashboardCommandCardReader(&dashboardCommandCardRoot{err: want})
	prompt := provideDashboardPromptTemplateReader(&dashboardPromptRoot{err: want})
	sharedRoot := &dashboardSharedFileStoreTestDouble{dashboardSharedFileReaderTestDouble: dashboardSharedFileReaderTestDouble{err: want}}
	shared := provideDashboardSharedFileReader(sharedRoot)
	writer := shared.(dashboard.SharedFileWriter)
	ops := []func() error{
		func() error { _, e := agent.List(ctx, ""); return e }, func() error { _, e := ai.List(ctx, dashboard.AILogFilter{}); return e }, func() error { _, e := ai.ListByCategory(ctx, "", "", 1); return e }, func() error { _, e := ai.CountByStatus(ctx); return e }, func() error { _, e := ai.ListRecent(ctx, 1); return e },
		func() error { _, e := audit.List(ctx, dashboard.AuditLogFilter{}); return e }, func() error { _, e := bus.List(ctx, dashboard.BusLogFilter{}); return e }, func() error { _, e := bus.Get(ctx, 1); return e }, func() error { _, e := system.List(ctx, dashboard.SystemLogFilter{}); return e },
		func() error { _, e := command.List(ctx, dashboard.CommandCardFilter{}); return e }, func() error { _, e := prompt.List(ctx, dashboard.PromptTemplateFilter{}); return e }, func() error { _, e := shared.Get(ctx, ""); return e }, func() error { _, e := shared.List(ctx, dashboard.SharedFileFilter{}); return e }, func() error { _, e := writer.Upsert(ctx, dashboard.SharedFileUpsertParams{}); return e },
	}
	for i, op := range ops {
		if err := op(); err != want || !errors.Is(err, want) {
			t.Fatalf("operation %d error=%v", i, err)
		}
	}
}

// TestDashboardSharedFileAdapterPreservesNilResults 固定 Get/Upsert 的 nil,nil 结果。
func TestDashboardSharedFileAdapterPreservesNilResults(t *testing.T) {
	root := &dashboardSharedFileStoreTestDouble{}
	port := provideDashboardSharedFileReader(root)
	if got, err := port.Get(context.Background(), "x"); got != nil || err != nil {
		t.Fatalf("Get=(%v,%v)", got, err)
	}
	if got, err := port.(dashboard.SharedFileWriter).Upsert(context.Background(), dashboard.SharedFileUpsertParams{}); got != nil || err != nil {
		t.Fatalf("Upsert=(%v,%v)", got, err)
	}
}

// TestBusinessStoreAdaptersModuleOwnsDashboardPorts 通过真实 Fx lifecycle 证明九个端口归 App bundle 所有。
func TestBusinessStoreAdaptersModuleOwnsDashboardPorts(t *testing.T) {
	agent, ai, audit := &dashboardAgentStatusRoot{}, &dashboardAILogRoot{}, &dashboardAuditLogRoot{}
	bus, system, query := &dashboardBusLogRoot{}, &dashboardSystemLogRoot{}, &dashboardDBQueryRoot{}
	command, prompt, shared := &dashboardCommandCardRoot{}, &dashboardPromptRoot{}, &dashboardSharedFileReaderTestDouble{}
	var p1 dashboard.AgentStatusReader
	var p2 dashboard.AILogReader
	var p3 dashboard.AuditLogReader
	var p4 dashboard.BusLogReader
	var p5 dashboard.SystemLogReader
	var p6 dashboard.DBQueryExecutor
	var p7 dashboard.CommandCardReader
	var p8 dashboard.PromptTemplateReader
	var p9 dashboard.SharedFileReader
	app := fx.New(fx.NopLogger,
		fx.Provide(func() agentstatusstore.Store { return agent }), fx.Provide(func() ailogstore.Store { return ai }),
		fx.Provide(func() auditlogstore.Store { return audit }), fx.Provide(func() buslogstore.Store { return bus }),
		fx.Provide(func() systemlogstore.Store { return system }), fx.Provide(func() dbquerystore.Store { return query }),
		fx.Provide(func() commandcardstore.Reader { return command }), fx.Provide(func() promptstore.Reader { return prompt }),
		fx.Provide(func() sharedfilestore.Reader { return shared }), businessStoreAdaptersModule(),
		fx.Populate(&p1, &p2, &p3, &p4, &p5, &p6, &p7, &p8, &p9))
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New: %v", err)
	}
	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("fx.Start: %v", err)
	}
	if err := app.Stop(ctx); err != nil {
		t.Fatalf("fx.Stop: %v", err)
	}
	for index, port := range []any{p1, p2, p3, p4, p5, p6, p7, p8, p9} {
		if port == nil {
			t.Fatalf("dashboard port %d is nil", index)
		}
	}
}
