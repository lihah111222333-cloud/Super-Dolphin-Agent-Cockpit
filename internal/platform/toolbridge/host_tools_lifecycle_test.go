package toolbridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

const testLifecycleWorkspaceRoot = "/tmp/super-agent-toolbridge-lifecycle"

type stubMCPToolLifecycleReader struct {
	records   []contract.MCPToolLifecycleRecord
	listErr   error
	listCalls []contract.MCPToolLifecycleListParams
}

func (s *stubMCPToolLifecycleReader) GetMCPToolLifecycleState(
	_ context.Context,
	key contract.MCPToolLifecycleKey,
) (contract.MCPToolLifecycleRecord, error) {
	for _, record := range s.records {
		if record.WorkspaceRoot == key.WorkspaceRoot &&
			record.ServerName == key.ServerName &&
			record.ToolName == key.ToolName {
			return record, nil
		}
	}
	return contract.MCPToolLifecycleRecord{}, errMCPToolLifecycleRowMissing
}

func (s *stubMCPToolLifecycleReader) ListMCPToolLifecycleStates(
	_ context.Context,
	params contract.MCPToolLifecycleListParams,
) ([]contract.MCPToolLifecycleRecord, error) {
	s.listCalls = append(s.listCalls, params)
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]contract.MCPToolLifecycleRecord, 0, len(s.records))
	for _, record := range s.records {
		if record.WorkspaceRoot == params.WorkspaceRoot && record.ServerName == params.ServerName {
			out = append(out, record)
		}
	}
	return out, nil
}

func TestListToolsForCodex_FiltersManagedPeerToolsByLifecycleState(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{records: []contract.MCPToolLifecycleRecord{
		testLifecycleRecord(dto.ClientKindOrch, "remote_active", contract.MCPToolLifecycleStateActive),
		testLifecycleRecord(dto.ClientKindOrch, "remote_suspended", contract.MCPToolLifecycleStateSuspended),
		testLifecycleRecord(dto.ClientKindOrch, "remote_removed", contract.MCPToolLifecycleStateRemoved),
	}}
	h := newLifecycleListHandler(
		reader,
		nil,
		[]dto.MCPTool{
			{Name: "remote_active", Description: "active"},
			{Name: "remote_suspended", Description: "suspended"},
			{Name: "remote_removed", Description: "removed"},
		},
		nil,
	)

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if !containsDynamicToolName(got, "remote_active") {
		t.Fatalf("tools = %+v, want active tool published", got)
	}
	for _, blocked := range []string{"remote_suspended", "remote_removed"} {
		if containsDynamicToolName(got, blocked) {
			t.Fatalf("tools = %+v, must not publish lifecycle-blocked tool %s", got, blocked)
		}
	}
	assertLifecycleListCall(t, reader, dto.ClientKindOrch)
}

func TestListToolsForCodex_FailsClosedWhenLifecycleRowMissing(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{records: []contract.MCPToolLifecycleRecord{
		testLifecycleRecord(dto.ClientKindOrch, "known_tool", contract.MCPToolLifecycleStateActive),
	}}
	h := newLifecycleListHandler(reader, nil, []dto.MCPTool{{Name: "missing_tool", Description: "missing"}}, nil)

	got, err := h.ListToolsForCodex(context.Background())
	assertListToolsLifecycleError(t, got, err, "MCP tool lifecycle row is missing", "missing_tool")
}

func TestListToolsForCodex_FailsClosedWhenLifecycleReaderErrors(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{listErr: errors.New("reader unavailable")}
	h := newLifecycleListHandler(reader, nil, []dto.MCPTool{{Name: "remote_tool", Description: "remote"}}, nil)

	got, err := h.ListToolsForCodex(context.Background())
	assertListToolsLifecycleError(t, got, err, "reader unavailable", dto.ClientKindOrch)
}

func TestListToolsForCodex_FailsClosedWhenLifecycleReaderMissing(t *testing.T) {
	h := newLifecycleListHandler(nil, nil, []dto.MCPTool{{Name: "remote_tool", Description: "remote"}}, nil)

	got, err := h.ListToolsForCodex(context.Background())
	assertListToolsLifecycleError(t, got, err, "MCP tool lifecycle reader is not configured")
}

func TestListToolsForCodex_FailsClosedWhenLifecycleProjectRootMissing(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{records: []contract.MCPToolLifecycleRecord{
		testLifecycleRecord(dto.ClientKindOrch, "remote_tool", contract.MCPToolLifecycleStateActive),
	}}
	h := newLifecycleListHandler(reader, nil, []dto.MCPTool{{Name: "remote_tool", Description: "remote"}}, nil)
	h.cfg.ProjectRoot = ""

	got, err := h.ListToolsForCodex(context.Background())
	assertListToolsLifecycleError(t, got, err, "MCP tool lifecycle project root is not configured")
	if len(reader.listCalls) != 0 {
		t.Fatalf("lifecycle list calls = %+v, want no reader call without project root", reader.listCalls)
	}
}

func TestFilterManagedPeerToolsByLifecycle_FailsClosedWhenServerNameMissing(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{}
	h := newLifecycleListHandler(reader, nil, nil, nil)

	got, err := h.filterManagedPeerToolsByLifecycle(context.Background(), " ", []dto.MCPTool{{Name: "remote_tool"}})
	assertListToolsLifecycleError(t, toCodexDynamicTools(got), err, "empty server name")
	if len(reader.listCalls) != 0 {
		t.Fatalf("lifecycle list calls = %+v, want no reader call without server name", reader.listCalls)
	}
}

func TestListToolsForCodex_FailsClosedWhenLifecycleStateUnknown(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{records: []contract.MCPToolLifecycleRecord{
		testLifecycleRecord(dto.ClientKindOrch, "remote_tool", contract.MCPToolLifecycleState("paused")),
	}}
	h := newLifecycleListHandler(reader, nil, []dto.MCPTool{{Name: "remote_tool", Description: "remote"}}, nil)

	got, err := h.ListToolsForCodex(context.Background())
	assertListToolsLifecycleError(t, got, err, "unknown MCP tool lifecycle state", "paused")
}

func TestListToolsForCodex_HostDirectIgnoresMCPToolLifecycleRows(t *testing.T) {
	reader := &stubMCPToolLifecycleReader{records: []contract.MCPToolLifecycleRecord{
		testLifecycleRecord(dto.ClientKindOrch, testHostToolName, contract.MCPToolLifecycleStateRemoved),
	}}
	host := &stubHostToolRegistry{tools: []dto.MCPTool{{Name: testHostToolName, Description: "host survives"}}}
	h := newLifecycleListHandler(reader, host, nil, nil)

	got, err := h.ListToolsForCodex(context.Background())
	if err != nil {
		t.Fatalf("ListToolsForCodex() error = %v", err)
	}
	if !containsDynamicToolName(got, testHostToolName) {
		t.Fatalf("tools = %+v, want host-direct tool preserved", got)
	}
	if len(reader.listCalls) != 0 {
		t.Fatalf("lifecycle list calls = %+v, want no lookup for host-only tools", reader.listCalls)
	}
}

func newLifecycleListHandler(
	reader *stubMCPToolLifecycleReader,
	host HostToolRegistry,
	orchTools []dto.MCPTool,
	lspTools []dto.MCPTool,
) *Handler {
	var lifecycleReader mcpToolLifecycleReader
	if reader != nil {
		lifecycleReader = reader
	}
	return &Handler{
		registry: &stubKindRegistry{peers: map[string][]*mcpcontrol.ToolInstance{
			dto.ClientKindOrch: {listToolsPeer(orchTools, nil)},
			dto.ClientKindLSP:  {listToolsPeer(lspTools, nil)},
		}},
		hostTools:           host,
		toolLifecycleReader: lifecycleReader,
		cfg:                 &platformconfig.Config{ProjectRoot: testLifecycleWorkspaceRoot},
	}
}

// attachActiveLifecycleForTools 给成功列表用例补齐 active 行，避免 managed peer 工具绕过 lifecycle 校验。
func attachActiveLifecycleForTools(h *Handler, toolsByServer map[string][]string) *stubMCPToolLifecycleReader {
	records := make([]contract.MCPToolLifecycleRecord, 0)
	for serverName, toolNames := range toolsByServer {
		for _, toolName := range toolNames {
			records = append(records, testLifecycleRecord(serverName, toolName, contract.MCPToolLifecycleStateActive))
		}
	}
	reader := &stubMCPToolLifecycleReader{records: records}
	h.toolLifecycleReader = reader
	if h.cfg == nil {
		h.cfg = &platformconfig.Config{}
	}
	h.cfg.ProjectRoot = testLifecycleWorkspaceRoot
	return reader
}

func testLifecycleRecord(
	serverName string,
	toolName string,
	state contract.MCPToolLifecycleState,
) contract.MCPToolLifecycleRecord {
	return contract.MCPToolLifecycleRecord{
		WorkspaceRoot: testLifecycleWorkspaceRoot,
		ServerName:    serverName,
		ToolName:      toolName,
		State:         state,
	}
}

func assertLifecycleListCall(t *testing.T, reader *stubMCPToolLifecycleReader, serverName string) {
	t.Helper()
	if len(reader.listCalls) != 1 {
		t.Fatalf("lifecycle list calls = %+v, want one call", reader.listCalls)
	}
	got := reader.listCalls[0]
	if got.WorkspaceRoot != testLifecycleWorkspaceRoot || got.ServerName != serverName {
		t.Fatalf("lifecycle list call = %+v, want workspace=%q server=%q", got, testLifecycleWorkspaceRoot, serverName)
	}
}

func assertListToolsLifecycleError(
	t *testing.T,
	got []contract.DynamicToolSchema,
	err error,
	fragments ...string,
) {
	t.Helper()
	if err == nil {
		t.Fatalf("ListToolsForCodex() error = nil, tools = %+v", got)
	}
	if len(got) != 0 {
		t.Fatalf("tools = %+v, want no partial dynamic surface on lifecycle failure", got)
	}
	text := err.Error()
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Fatalf("ListToolsForCodex() error = %v, want fragment %q", err, fragment)
		}
	}
}
