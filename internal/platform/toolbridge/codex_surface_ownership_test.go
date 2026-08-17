package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type ownershipMCPClient struct {
	tools     []mcpdto.MCPTool
	listErr   error
	closeErr  error
	closeCall int
}

func (c *ownershipMCPClient) ListTools(context.Context) ([]mcpdto.MCPTool, error) {
	return append([]mcpdto.MCPTool(nil), c.tools...), c.listErr
}

func (c *ownershipMCPClient) CallTool(context.Context, string, json.RawMessage, ToolCallRequest) (*ToolCallResult, error) {
	return nil, errors.New("unexpected tool call")
}

func (c *ownershipMCPClient) Close() error {
	c.closeCall++
	return c.closeErr
}

type ownershipLifecycle struct {
	backfillErr error
	resolveErr  error
}

func (o ownershipLifecycle) BackfillMCPTools(context.Context, MCPToolLifecycleBackfillRequest) error {
	return o.backfillErr
}

func (o ownershipLifecycle) ResolveMCPToolLifecycle(context.Context, contract.MCPToolLifecyclePolicyRequest) (contract.MCPToolLifecycleDecision, error) {
	if o.resolveErr != nil {
		return contract.MCPToolLifecycleDecision{}, o.resolveErr
	}
	return contract.MCPToolLifecycleDecision{State: contract.MCPToolLifecycleEnabled}, nil
}

func TestPrepareMCPSurfaceBinariesJoinsPrimaryAndEveryCloseError(t *testing.T) {
	primaryErr := errors.New("list tools failed")
	closeErrs := []error{errors.New("close one failed"), errors.New("close two failed"), errors.New("close three failed")}
	clients := ownershipClients(closeErrs)
	clients[1].listErr = primaryErr
	started := make(chan struct{})
	var startedCount atomic.Int32
	baseFactory := ownershipClientFactory(clients, nil)
	factory := func(ctx context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
		client, err := baseFactory(ctx, binary)
		if startedCount.Add(1) == int32(len(clients)) {
			close(started)
		}
		select {
		case <-started:
			return client, err
		case <-ctx.Done():
			return client, ctx.Err()
		}
	}
	results, err := prepareMCPSurfaceBinaries(context.Background(), factory, ownershipBinaries(len(clients)))
	if results != nil || !errors.Is(err, primaryErr) {
		t.Fatalf("prepare result = %#v, error = %v, want list failure", results, err)
	}
	assertOwnershipClientsClosedOnce(t, clients)
	for _, closeErr := range closeErrs {
		if !errors.Is(err, closeErr) {
			t.Fatalf("prepare error = %v, want close error %v", err, closeErr)
		}
	}
}

func TestPrepareMCPSurfaceBinariesClosesClientReturnedWithFactoryError(t *testing.T) {
	primaryErr := errors.New("factory failed after allocation")
	client := &ownershipMCPClient{closeErr: errors.New("allocated client close failed")}
	_, err := prepareMCPSurfaceBinaries(
		context.Background(),
		func(context.Context, providerdto.MCPBinary) (mcpClient, error) { return client, primaryErr },
		ownershipBinaries(1),
	)
	if !errors.Is(err, primaryErr) || !errors.Is(err, client.closeErr) {
		t.Fatalf("prepare error = %v, want factory and close failures", err)
	}
	if client.closeCall != 1 {
		t.Fatalf("client close count = %d, want 1", client.closeCall)
	}
}

func TestPrepareMCPSurfaceBinariesRejectsOverLimitBeforeFactory(t *testing.T) {
	var calls atomic.Int32
	results, err := prepareMCPSurfaceBinaries(context.Background(), func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		calls.Add(1)
		return &ownershipMCPClient{}, nil
	}, ownershipBinaries(maxMCPSurfaceBinaries+1))
	if err == nil || results != nil || calls.Load() != 0 {
		t.Fatalf("over-limit result=%v error=%v factory calls=%d, want fail before factory", results, err, calls.Load())
	}
}

func TestPrepareMCPSurfaceBinariesBoundsActiveFactories(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	factory := func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		current := active.Add(1)
		for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		active.Add(-1)
		return &ownershipMCPClient{}, nil
	}
	results, err := prepareMCPSurfaceBinaries(context.Background(), factory, ownershipBinaries(12))
	if err != nil || len(results) != 12 {
		t.Fatalf("bounded prepare result=%d error=%v", len(results), err)
	}
	if got := peak.Load(); got < 2 || got > maxConcurrentMCPInitializers {
		t.Fatalf("peak active factories=%d, want 2..%d", got, maxConcurrentMCPInitializers)
	}
}

func TestPrepareCodexToolSurfaceTransfersAllClientOwnershipBeforePostProcessing(t *testing.T) {
	for _, tc := range []struct {
		name      string
		lifecycle *ownershipLifecycle
		tools     [][]mcpdto.MCPTool
		wantErr   error
	}{
		{name: "backfill", lifecycle: &ownershipLifecycle{backfillErr: errors.New("backfill failed")}, wantErr: errors.New("backfill failed")},
		{name: "filter", lifecycle: &ownershipLifecycle{resolveErr: errors.New("filter failed")}, wantErr: errors.New("filter failed")},
		{name: "schema", tools: [][]mcpdto.MCPTool{{{Name: "duplicate"}, {Name: "duplicate"}}}, wantErr: errors.New("duplicate tool name")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closeErrs := []error{errors.New("close one failed"), errors.New("close two failed"), errors.New("close three failed")}
			clients := ownershipClients(closeErrs)
			applyOwnershipTools(clients, tc.tools)
			h := &Handler{stdioClientFactory: ownershipClientFactory(clients, nil)}
			if tc.lifecycle != nil {
				h.lifecycle = tc.lifecycle
				h.lifecyclePolicy = tc.lifecycle
			}
			_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), ownershipScope(len(clients)))
			if err == nil || !containsOwnershipError(err, tc.wantErr) {
				t.Fatalf("PrepareCodexToolSurface() error = %v, want %v", err, tc.wantErr)
			}
			assertOwnershipClientsClosedOnce(t, clients)
			for _, closeErr := range closeErrs {
				if !errors.Is(err, closeErr) {
					t.Fatalf("prepare error = %v, want close error %v", err, closeErr)
				}
			}
		})
	}
}

func TestCodexToolSurfaceCloseAggregatesAndClosesClientsOnce(t *testing.T) {
	closeErrs := []error{errors.New("close one failed"), errors.New("close two failed"), errors.New("close three failed")}
	clients := ownershipClients(closeErrs)
	surface := &codexToolSurface{clients: ownershipClientInterfaces(clients)}
	err := surface.Close()
	for _, closeErr := range closeErrs {
		if !errors.Is(err, closeErr) {
			t.Fatalf("Close() error = %v, want %v", err, closeErr)
		}
	}
	if secondErr := surface.Close(); !errors.Is(secondErr, closeErrs[0]) {
		t.Fatalf("second Close() error = %v, want stable aggregate", secondErr)
	}
	assertOwnershipClientsClosedOnce(t, clients)
}

func TestPrepareCodexToolSurfaceJoinsReplacementAndNewSurfaceCloseErrors(t *testing.T) {
	oldCloseErr := errors.New("close replaced surface failed")
	newCloseErr := errors.New("close new surface failed")
	oldClient := &ownershipMCPClient{tools: []mcpdto.MCPTool{{Name: "old_tool"}}, closeErr: oldCloseErr}
	newClient := &ownershipMCPClient{tools: []mcpdto.MCPTool{{Name: "new_tool"}}, closeErr: newCloseErr}
	clients := []*ownershipMCPClient{oldClient, newClient}
	next := 0
	h := &Handler{stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		client := clients[next]
		next++
		return client, nil
	}}
	scope := ownershipScope(1)
	if _, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), scope); err != nil {
		t.Fatalf("PrepareCodexToolSurface(old) error = %v", err)
	}
	_, err := prepareCodexToolSurfaceForTest(t, h, context.Background(), scope)
	if !errors.Is(err, oldCloseErr) || !errors.Is(err, newCloseErr) {
		t.Fatalf("PrepareCodexToolSurface(new) error = %v, want replaced and new close failures", err)
	}
	assertOwnershipClientsClosedOnce(t, clients)
	if h.surfaces["agent-1"] != nil {
		t.Fatal("failed replacement left a closed new surface indexed")
	}
}

func TestStoreCodexToolSurfaceClosesEveryReplacedSurfaceAfterCloseErrors(t *testing.T) {
	firstErr := errors.New("close replaced one failed")
	secondErr := errors.New("close replaced two failed")
	for _, tc := range []struct {
		name      string
		closeErrs []error
		wantErrs  []error
	}{
		{name: "first", closeErrs: []error{firstErr, nil}, wantErrs: []error{firstErr}},
		{name: "second", closeErrs: []error{nil, secondErr}, wantErrs: []error{secondErr}},
		{name: "both", closeErrs: []error{firstErr, secondErr}, wantErrs: []error{firstErr, secondErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, clients, surface := replacementOwnershipFixture(tc.closeErrs)
			err := h.storeCodexToolSurface(surface)
			for _, wantErr := range tc.wantErrs {
				if !errors.Is(err, wantErr) {
					t.Fatalf("storeCodexToolSurface() error = %v, want %v", err, wantErr)
				}
			}
			assertOwnershipClientsClosedOnce(t, clients)
		})
	}
}

func TestStoreCodexToolSurfaceKeepsNewSurfaceWhenAllReplacedClose(t *testing.T) {
	h, clients, surface := replacementOwnershipFixture([]error{nil, nil})
	if err := h.storeCodexToolSurface(surface); err != nil {
		t.Fatalf("storeCodexToolSurface() error = %v", err)
	}
	assertOwnershipClientsClosedOnce(t, clients)
	for _, key := range surface.keys {
		if h.surfaces[key] != surface {
			t.Fatalf("surface key %q is not bound to new surface", key)
		}
	}
}

func ownershipClients(closeErrs []error) []*ownershipMCPClient {
	clients := make([]*ownershipMCPClient, 0, len(closeErrs))
	for i, closeErr := range closeErrs {
		clients = append(clients, &ownershipMCPClient{
			tools:    []mcpdto.MCPTool{{Name: fmt.Sprintf("tool_%d", i), InputSchema: json.RawMessage(`{"type":"object"}`)}},
			closeErr: closeErr,
		})
	}
	return clients
}

func replacementOwnershipFixture(closeErrs []error) (*Handler, []*ownershipMCPClient, *codexToolSurface) {
	clients := ownershipClients(closeErrs)
	oldOne := &codexToolSurface{keys: []string{"agent-1"}, clients: []mcpClient{clients[0]}}
	oldTwo := &codexToolSurface{keys: []string{"provider-thread-1"}, clients: []mcpClient{clients[1]}}
	h := &Handler{surfaces: map[string]*codexToolSurface{"agent-1": oldOne, "provider-thread-1": oldTwo}}
	return h, clients, &codexToolSurface{keys: []string{"agent-1", "provider-thread-1"}}
}

func ownershipClientInterfaces(clients []*ownershipMCPClient) []mcpClient {
	out := make([]mcpClient, 0, len(clients))
	for _, client := range clients {
		out = append(out, client)
	}
	return out
}

func ownershipClientFactory(clients []*ownershipMCPClient, factoryErr error) func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
	return func(_ context.Context, binary providerdto.MCPBinary) (mcpClient, error) {
		for i := range clients {
			if binary.Name == fmt.Sprintf("server-%d", i) {
				return clients[i], factoryErr
			}
		}
		return nil, errors.New("unknown binary")
	}
}

func ownershipBinaries(count int) []providerdto.MCPBinary {
	out := make([]providerdto.MCPBinary, 0, count)
	for i := range count {
		out = append(out, providerdto.MCPBinary{Name: fmt.Sprintf("server-%d", i), Command: []string{"mcp-server"}})
	}
	return out
}

func ownershipScope(count int) contract.CodexToolSurfaceScope {
	return contract.CodexToolSurfaceScope{AgentID: "agent-1", CWD: portableToolbridgeTestCWD("ownership", "repo"), Manifest: providerdto.MCPManifest{Binaries: ownershipBinaries(count)}}
}

func applyOwnershipTools(clients []*ownershipMCPClient, tools [][]mcpdto.MCPTool) {
	for i := range tools {
		clients[i].tools = tools[i]
	}
}

func assertOwnershipClientsClosedOnce(t *testing.T, clients []*ownershipMCPClient) {
	t.Helper()
	for i, client := range clients {
		if client.closeCall != 1 {
			t.Fatalf("client %d close count = %d, want 1", i, client.closeCall)
		}
	}
}

func containsOwnershipError(err, want error) bool {
	return errors.Is(err, want) || (err != nil && want != nil && strings.Contains(err.Error(), want.Error()))
}
