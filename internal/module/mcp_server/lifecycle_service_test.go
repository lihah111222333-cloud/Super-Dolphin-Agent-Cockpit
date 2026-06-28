package mcpserver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func TestMCPToolLifecycleOwnerAPIUpsertGetAndList(t *testing.T) {
	configStore := newMemoryMCPServerStore()
	lifecycleStore := newMemoryMCPToolLifecycleStore()
	svc := NewServiceWithStores(configStore, lifecycleStore)
	project := t.TempDir()
	t.Chdir(project)
	configStore.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	ctx := context.Background()
	key := contract.MCPToolLifecycleKey{
		WorkspaceRoot: project,
		ServerName:    "my-search",
		ToolName:      "remote_search",
	}

	record, err := svc.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key:       key,
		State:     contract.MCPToolLifecycleStateSuspended,
		Reason:    "operator pause",
		Source:    contract.MCPToolLifecycleSourceUser,
		UpdatedBy: "operator",
	})
	if err != nil {
		t.Fatalf("UpsertMCPToolLifecycleState() error = %v", err)
	}
	assertLifecycleRecordStateSource(
		t,
		record,
		contract.MCPToolLifecycleStateSuspended,
		contract.MCPToolLifecycleSourceUser,
	)

	got, err := svc.GetMCPToolLifecycleState(ctx, key)
	if err != nil {
		t.Fatalf("GetMCPToolLifecycleState() error = %v", err)
	}
	assertLifecycleRecordFields(t, got, "remote_search", "operator pause", "operator")

	_, err = svc.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key: contract.MCPToolLifecycleKey{
			WorkspaceRoot: project,
			ServerName:    "my-search",
			ToolName:      "remote_inspect",
		},
		State:  contract.MCPToolLifecycleStateActive,
		Source: contract.MCPToolLifecycleSourceDiscovery,
	})
	if err != nil {
		t.Fatalf("UpsertMCPToolLifecycleState(second) error = %v", err)
	}

	records, err := svc.ListMCPToolLifecycleStates(ctx, contract.MCPToolLifecycleListParams{
		WorkspaceRoot: project,
		ServerName:    "my-search",
	})
	if err != nil {
		t.Fatalf("ListMCPToolLifecycleStates() error = %v", err)
	}
	if gotTools := lifecycleToolNames(records); !slices.Equal(gotTools, []string{"remote_inspect", "remote_search"}) {
		t.Fatalf("listed tools = %v, want sorted lifecycle rows", gotTools)
	}
}

func TestMCPToolLifecycleOwnerAPIFailsFastOnInvalidDependenciesAndParams(t *testing.T) {
	project := t.TempDir()
	validKey := contract.MCPToolLifecycleKey{
		WorkspaceRoot: project,
		ServerName:    "my-search",
		ToolName:      "remote_search",
	}
	tests := []struct {
		name    string
		svc     Service
		params  contract.MCPToolLifecycleUpsertParams
		wantErr string
	}{
		{
			name:    "missing config store",
			svc:     NewServiceWithStores(nil, newMemoryMCPToolLifecycleStore()),
			params:  validLifecycleUpsertParams(validKey),
			wantErr: "config store is not configured",
		},
		{
			name:    "missing lifecycle store",
			svc:     NewServiceWithStore(seedMemoryConfigStore(project, "my-search")),
			params:  validLifecycleUpsertParams(validKey),
			wantErr: "tool lifecycle store is not configured",
		},
		{
			name: "empty workspace",
			svc:  NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore()),
			params: mutateLifecycleUpsertParams(validLifecycleUpsertParams(validKey), func(p *contract.MCPToolLifecycleUpsertParams) {
				p.Key.WorkspaceRoot = " "
			}),
			wantErr: "workspaceRoot is required",
		},
		{
			name: "empty server",
			svc:  NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore()),
			params: mutateLifecycleUpsertParams(validLifecycleUpsertParams(validKey), func(p *contract.MCPToolLifecycleUpsertParams) {
				p.Key.ServerName = ""
			}),
			wantErr: "server name is required",
		},
		{
			name: "empty tool",
			svc:  NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore()),
			params: mutateLifecycleUpsertParams(validLifecycleUpsertParams(validKey), func(p *contract.MCPToolLifecycleUpsertParams) {
				p.Key.ToolName = "\t"
			}),
			wantErr: "tool name is required",
		},
		{
			name: "invalid state",
			svc:  NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore()),
			params: mutateLifecycleUpsertParams(validLifecycleUpsertParams(validKey), func(p *contract.MCPToolLifecycleUpsertParams) {
				p.State = "paused"
			}),
			wantErr: "invalid lifecycle state",
		},
		{
			name: "invalid source",
			svc:  NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore()),
			params: mutateLifecycleUpsertParams(validLifecycleUpsertParams(validKey), func(p *contract.MCPToolLifecycleUpsertParams) {
				p.Source = "fallback"
			}),
			wantErr: "invalid lifecycle source",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertLifecycleUpsertError(t, tt.svc, tt.params, tt.wantErr)
		})
	}
}

func TestListServerToolsBackfillsLifecycleWithoutOverwritingUserState(t *testing.T) {
	configStore := newMemoryMCPServerStore()
	lifecycleStore := newMemoryMCPToolLifecycleStore()
	svc := NewServiceWithStores(configStore, lifecycleStore).(*service)
	svc.httpClient = &scriptedMCPHTTPDoer{
		t:         t,
		toolNames: []string{"remote_search", "remote_inspect"},
	}
	project := t.TempDir()
	t.Chdir(project)
	configStore.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	ctx := context.Background()
	_, err := svc.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key: contract.MCPToolLifecycleKey{
			WorkspaceRoot: project,
			ServerName:    "my-search",
			ToolName:      "remote_search",
		},
		State:  contract.MCPToolLifecycleStateSuspended,
		Reason: "operator pause",
		Source: contract.MCPToolLifecycleSourceUser,
	})
	if err != nil {
		t.Fatalf("seed suspended lifecycle: %v", err)
	}

	got, err := svc.ListServerTools(ctx, ListServerToolsRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("ListServerTools() error = %v", err)
	}
	assertMCPToolNames(t, got.Tools, []string{"remote_search", "remote_inspect"})

	search, err := svc.GetMCPToolLifecycleState(ctx, contract.MCPToolLifecycleKey{
		WorkspaceRoot: project,
		ServerName:    "my-search",
		ToolName:      "remote_search",
	})
	if err != nil {
		t.Fatalf("GetMCPToolLifecycleState(search) error = %v", err)
	}
	assertLifecycleRecordStateReason(t, search, contract.MCPToolLifecycleStateSuspended, "operator pause")
	inspect, err := svc.GetMCPToolLifecycleState(ctx, contract.MCPToolLifecycleKey{
		WorkspaceRoot: project,
		ServerName:    "my-search",
		ToolName:      "remote_inspect",
	})
	if err != nil {
		t.Fatalf("GetMCPToolLifecycleState(inspect) error = %v", err)
	}
	assertLifecycleRecordStateSource(
		t,
		inspect,
		contract.MCPToolLifecycleStateActive,
		contract.MCPToolLifecycleSourceDiscovery,
	)
}

func TestListServerToolsKeepsCompatibilityWhenLifecycleStoreMissing(t *testing.T) {
	configStore := newMemoryMCPServerStore()
	client := &scriptedMCPHTTPDoer{t: t}
	svc := NewServiceWithStore(configStore).(*service)
	svc.httpClient = client
	project := t.TempDir()
	t.Chdir(project)
	configStore.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})

	got, err := svc.ListServerTools(context.Background(), ListServerToolsRequest{ServerName: "my-search"})
	if err != nil {
		t.Fatalf("ListServerTools() error = %v, want old tools/list compatibility", err)
	}
	assertMCPToolNames(t, got.Tools, []string{"remote_search"})
}

func TestBackfillDiscoveredMCPToolLifecycleStatesValidatesServerAndToolNames(t *testing.T) {
	project := t.TempDir()
	svc := NewServiceWithStores(seedMemoryConfigStore(project, "my-search"), newMemoryMCPToolLifecycleStore())

	_, err := svc.BackfillDiscoveredMCPToolLifecycleStates(context.Background(), BackfillMCPToolLifecycleRequest{
		WorkspaceRoot: project,
		ServerName:    "missing",
		Tools:         []mcpdto.MCPTool{{Name: "remote_search"}},
	})
	if !errors.Is(err, errServerNotFound) {
		t.Fatalf("BackfillDiscoveredMCPToolLifecycleStates(missing server) error = %v, want errServerNotFound", err)
	}

	_, err = svc.BackfillDiscoveredMCPToolLifecycleStates(context.Background(), BackfillMCPToolLifecycleRequest{
		WorkspaceRoot: project,
		ServerName:    "my-search",
		Tools:         []mcpdto.MCPTool{{Name: " "}},
	})
	if err == nil || !strings.Contains(err.Error(), "tool name is required") {
		t.Fatalf("BackfillDiscoveredMCPToolLifecycleStates(empty tool) error = %v, want tool name validation", err)
	}
}

func validLifecycleUpsertParams(key contract.MCPToolLifecycleKey) contract.MCPToolLifecycleUpsertParams {
	return contract.MCPToolLifecycleUpsertParams{
		Key:    key,
		State:  contract.MCPToolLifecycleStateActive,
		Source: contract.MCPToolLifecycleSourceUser,
	}
}

func assertLifecycleRecordStateSource(
	t *testing.T,
	record contract.MCPToolLifecycleRecord,
	state contract.MCPToolLifecycleState,
	source contract.MCPToolLifecycleSource,
) {
	t.Helper()
	if record.State != state || record.Source != source {
		t.Fatalf("record = %+v, want state=%s source=%s", record, state, source)
	}
}

func assertLifecycleRecordFields(
	t *testing.T,
	record contract.MCPToolLifecycleRecord,
	toolName string,
	reason string,
	updatedBy string,
) {
	t.Helper()
	if record.ToolName != toolName || record.Reason != reason || record.UpdatedBy != updatedBy {
		t.Fatalf("record = %+v, want persisted fields", record)
	}
}

func assertLifecycleUpsertError(
	t *testing.T,
	svc Service,
	params contract.MCPToolLifecycleUpsertParams,
	wantErr string,
) {
	t.Helper()
	_, err := svc.UpsertMCPToolLifecycleState(context.Background(), params)
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("UpsertMCPToolLifecycleState() error = %v, want %q", err, wantErr)
	}
}

func assertMCPToolNames(t *testing.T, tools []mcpdto.MCPTool, want []string) {
	t.Helper()
	if gotTools := mcpToolNames(tools); !slices.Equal(gotTools, want) {
		t.Fatalf("MCP tool names = %v, want %v", gotTools, want)
	}
}

func assertLifecycleRecordStateReason(
	t *testing.T,
	record contract.MCPToolLifecycleRecord,
	state contract.MCPToolLifecycleState,
	reason string,
) {
	t.Helper()
	if record.State != state || record.Reason != reason {
		t.Fatalf("record = %+v, want state=%s reason=%q", record, state, reason)
	}
}

func mutateLifecycleUpsertParams(
	params contract.MCPToolLifecycleUpsertParams,
	mutate func(*contract.MCPToolLifecycleUpsertParams),
) contract.MCPToolLifecycleUpsertParams {
	mutate(&params)
	return params
}

func seedMemoryConfigStore(project, serverName string) *memoryMCPServerStore {
	store := newMemoryMCPServerStore()
	store.seed(project, serverName, ServerConfig{
		Transport: "http",
		URL:       "https://example.com/mcp",
	})
	return store
}

func lifecycleToolNames(records []contract.MCPToolLifecycleRecord) []string {
	names := make([]string, 0, len(records))
	for _, record := range records {
		names = append(names, record.ToolName)
	}
	return names
}

func mcpToolNames(tools []mcpdto.MCPTool) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}

type memoryMCPToolLifecycleStore struct {
	records map[string]contract.MCPToolLifecycleRecord
}

func newMemoryMCPToolLifecycleStore() *memoryMCPToolLifecycleStore {
	return &memoryMCPToolLifecycleStore{records: map[string]contract.MCPToolLifecycleRecord{}}
}

func (s *memoryMCPToolLifecycleStore) UpsertMCPToolLifecycleState(
	_ context.Context,
	params contract.MCPToolLifecycleUpsertParams,
) (contract.MCPToolLifecycleRecord, error) {
	if s.records == nil {
		s.records = map[string]contract.MCPToolLifecycleRecord{}
	}
	now := time.UnixMilli(1).UTC()
	id := lifecycleMemoryKey(params.Key)
	createdAt := now
	if existing, ok := s.records[id]; ok {
		createdAt = existing.CreatedAt
	}
	record := contract.MCPToolLifecycleRecord{
		WorkspaceRoot: params.Key.WorkspaceRoot,
		ServerName:    params.Key.ServerName,
		ToolName:      params.Key.ToolName,
		State:         params.State,
		Reason:        strings.TrimSpace(params.Reason),
		Source:        params.Source,
		UpdatedBy:     strings.TrimSpace(params.UpdatedBy),
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}
	s.records[id] = record
	return record, nil
}

func (s *memoryMCPToolLifecycleStore) EnsureDiscoveredMCPToolLifecycleState(
	ctx context.Context,
	params contract.MCPToolLifecycleDiscoveryParams,
) (contract.MCPToolLifecycleRecord, bool, error) {
	if s.records == nil {
		s.records = map[string]contract.MCPToolLifecycleRecord{}
	}
	id := lifecycleMemoryKey(params.Key)
	if existing, ok := s.records[id]; ok {
		return existing, false, nil
	}
	record, err := s.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key:       params.Key,
		State:     contract.MCPToolLifecycleStateActive,
		Reason:    params.Reason,
		Source:    contract.MCPToolLifecycleSourceDiscovery,
		UpdatedBy: params.UpdatedBy,
	})
	return record, true, err
}

func (s *memoryMCPToolLifecycleStore) GetMCPToolLifecycleState(
	_ context.Context,
	key contract.MCPToolLifecycleKey,
) (contract.MCPToolLifecycleRecord, error) {
	record, ok := s.records[lifecycleMemoryKey(key)]
	if !ok {
		return contract.MCPToolLifecycleRecord{}, errors.New("not found")
	}
	return record, nil
}

func (s *memoryMCPToolLifecycleStore) ListMCPToolLifecycleStates(
	_ context.Context,
	params contract.MCPToolLifecycleListParams,
) ([]contract.MCPToolLifecycleRecord, error) {
	records := make([]contract.MCPToolLifecycleRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.WorkspaceRoot == params.WorkspaceRoot && record.ServerName == params.ServerName {
			records = append(records, record)
		}
	}
	slices.SortFunc(records, func(a, b contract.MCPToolLifecycleRecord) int {
		return strings.Compare(a.ToolName, b.ToolName)
	})
	return records, nil
}

func lifecycleMemoryKey(key contract.MCPToolLifecycleKey) string {
	return key.WorkspaceRoot + "\x00" + key.ServerName + "\x00" + key.ToolName
}
