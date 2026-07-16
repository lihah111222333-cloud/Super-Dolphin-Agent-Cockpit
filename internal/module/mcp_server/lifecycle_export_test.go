package mcpserver

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestExportMCPToolLifecyclePreservesRollbackStates(t *testing.T) {
	store := newMemoryMCPServerStore()
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	store.seed(project, "my-search", ServerConfig{
		Transport: "http",
		URL:       "https://your-domain.com/mcp",
	})

	setLifecycleForExport(t, svc, SetMCPToolLifecycleRequest{
		ServerName:      "my-search",
		ManifestName:    "manifest-search",
		ToolName:        "search",
		State:           contract.MCPToolLifecycleRemoved,
		Reason:          "replaced by search_v2",
		ReplacementTool: "search_v2",
	})
	setLifecycleForExport(t, svc, SetMCPToolLifecycleRequest{
		ServerName: mcpdto.ClientKindLSP,
		ToolName:   "grep",
		State:      contract.MCPToolLifecycleSuspended,
		Reason:     "policy review",
	})

	got, err := svc.ExportMCPToolLifecycle(context.Background(), ExportMCPToolLifecycleRequest{})
	if err != nil {
		t.Fatalf("ExportMCPToolLifecycle() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ExportMCPToolLifecycle() len = %d, want 2: %#v", len(got), got)
	}
	assertExportMCPToolLifecycleDecision(t, got[0], mcpdto.ClientKindLSP, contract.MCPToolLifecycleSuspended, "", contract.MCPToolLifecycleDenyCodeSuspended)
	assertExportMCPToolLifecycleDecision(t, got[1], "my-search", contract.MCPToolLifecycleRemoved, "search_v2", contract.MCPToolLifecycleDenyCodeRemoved)
}

func setLifecycleForExport(t *testing.T, svc Service, req SetMCPToolLifecycleRequest) {
	t.Helper()
	if _, err := svc.SetMCPToolLifecycle(context.Background(), req); err != nil {
		t.Fatalf("SetMCPToolLifecycle(%s/%s) error = %v", req.ServerName, req.ToolName, err)
	}
}

func assertExportMCPToolLifecycleDecision(
	t *testing.T,
	got contract.MCPToolLifecycleDecision,
	serverName string,
	state contract.MCPToolLifecycleState,
	replacementTool string,
	denyCode string,
) {
	t.Helper()
	if got.ServerName != serverName {
		t.Fatalf("export server = %q, want %q; row=%#v", got.ServerName, serverName, got)
	}
	if got.State != state {
		t.Fatalf("export state = %q, want %q; row=%#v", got.State, state, got)
	}
	if got.ReplacementTool != replacementTool {
		t.Fatalf("export replacement = %q, want %q; row=%#v", got.ReplacementTool, replacementTool, got)
	}
	if got.DenyCode != denyCode {
		t.Fatalf("export denyCode = %q, want %q; row=%#v", got.DenyCode, denyCode, got)
	}
}

func TestTask4BReservedNameCannotForgeManagedAuthority(t *testing.T) {
	owner := AsMCPToolAuthorityOwner(NewService())
	_, err := owner.IssueMCPToolAuthority(context.Background(), contract.MCPToolAuthorityIssueRequest{
		CWD: "/repo",
		Binary: providerdto.MCPBinary{
			Name: "lsp", TrustedServerID: "lsp", Type: "http", URL: "http://attacker.invalid/mcp",
		},
		MembershipDigest: "membership",
	})
	if err == nil {
		t.Fatal("IssueMCPToolAuthority() error = nil, want reserved-name provenance rejection")
	}
}

func TestTask4BBuiltInManifestOwnerCanIssueManagedAuthority(t *testing.T) {
	manifest := contract.BuildManifest(providerdto.ManifestContext{
		AgentID: "agent", ProxyHTTPAddr: "127.0.0.1:9000",
	})
	if len(manifest.Binaries) == 0 || !manifest.Binaries[0].IsManagedMCPBinary() {
		t.Fatal("BuildManifest() did not retain built-in manifest-owner provenance")
	}
	owner := AsMCPToolAuthorityOwner(NewService())
	token, err := owner.IssueMCPToolAuthority(context.Background(), contract.MCPToolAuthorityIssueRequest{
		CWD: "/repo", Binary: manifest.Binaries[0], MembershipDigest: "membership",
	})
	if err != nil {
		t.Fatalf("IssueMCPToolAuthority() error = %v", err)
	}
	if !token.Managed || token.ServerID != manifest.Binaries[0].Name {
		t.Fatalf("managed token = %+v", token)
	}
}

func TestTask4BOwnerRestartAndSupersededGenerationFailClosed(t *testing.T) {
	ctx := context.Background()
	binary := providerdto.NewManagedMCPBinary(providerdto.MCPBinary{
		Name: "lsp", Type: "http", URL: "http://127.0.0.1:9000/mcp/lsp/agent",
	})
	owner := AsMCPToolAuthorityOwner(NewService())
	oldToken, err := owner.IssueMCPToolAuthority(ctx, contract.MCPToolAuthorityIssueRequest{
		CWD: "/repo", Binary: binary, MembershipDigest: "old-membership",
	})
	if err != nil {
		t.Fatalf("issue old authority: %v", err)
	}
	if err := AsMCPToolAuthorityOwner(NewService()).CheckMCPToolAuthority(ctx, oldToken); err == nil {
		t.Fatal("fresh owner accepted pre-restart token")
	}
	if _, err := owner.IssueMCPToolAuthority(ctx, contract.MCPToolAuthorityIssueRequest{
		CWD: "/repo", Binary: binary, MembershipDigest: "new-membership",
	}); err != nil {
		t.Fatalf("issue replacement authority: %v", err)
	}
	published := false
	err = owner.CompareAndSwapMCPToolQuarantines(ctx, []contract.MCPToolQuarantineCommit{{
		Authority: oldToken, Tools: map[string]string{"bad": "code"},
	}}, func() error {
		published = true
		return nil
	})
	if err == nil || published {
		t.Fatalf("stale CAS error/published = %v/%v, want rejection/no publish", err, published)
	}
}

func TestTask4BExternalAuthorityGenerationConfigAndMembershipCAS(t *testing.T) {
	store := newMemoryMCPServerStore()
	cwd := t.TempDir()
	binary := providerdto.MCPBinary{
		Name: "external", TrustedServerID: "external", Type: "http", URL: "https://example.test/v1",
	}
	store.seed(cwd, binary.Name, ServerConfig{Transport: "http", URL: binary.URL})
	owner := AsMCPToolAuthorityOwner(NewServiceWithStore(store))
	second, third := assertTask4BExternalAuthorityGenerations(t, store, owner, cwd, binary)
	assertTask4BExternalAuthorityCAS(t, owner, second, third)
}

func TestAuthorityDeleteAfterReadPreventsPublish(t *testing.T) {
	store := newMemoryMCPServerStore()
	cwd := t.TempDir()
	t.Chdir(cwd)
	binary := providerdto.MCPBinary{Name: "external", TrustedServerID: "external", Type: "http", URL: "https://example.test/v1"}
	store.seed(cwd, binary.Name, ServerConfig{Transport: "http", URL: binary.URL})
	svc := NewServiceWithStore(store)
	owner := AsMCPToolAuthorityOwner(svc)
	token := issueTask4BExternalAuthority(t, owner, cwd, binary, "membership")
	if _, err := svc.DeleteServer(context.Background(), DeleteServerRequest{ServerName: binary.Name}); err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	published := 0
	err := owner.CompareAndSwapMCPToolQuarantines(context.Background(), []contract.MCPToolQuarantineCommit{{Authority: token}}, func() error {
		published++
		return nil
	})
	if err == nil || published != 0 {
		t.Fatalf("stale publish error=%v count=%d, want rejection/0", err, published)
	}
}

func TestAuthorityDeleteAfterFinalCheckPreventsClientCall(t *testing.T) {
	store := newMemoryMCPServerStore()
	cwd := t.TempDir()
	t.Chdir(cwd)
	binary := providerdto.MCPBinary{Name: "external", TrustedServerID: "external", Type: "http", URL: "https://example.test/v1"}
	store.seed(cwd, binary.Name, ServerConfig{Transport: "http", URL: binary.URL})
	svc := NewServiceWithStore(store)
	owner := AsMCPToolAuthorityOwner(svc)
	token := issueTask4BExternalAuthority(t, owner, cwd, binary, "membership")
	if err := owner.CheckMCPToolAuthority(context.Background(), token); err != nil {
		t.Fatalf("final CheckMCPToolAuthority() error = %v", err)
	}
	if _, err := svc.DeleteServer(context.Background(), DeleteServerRequest{ServerName: binary.Name}); err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	clientCalls := 0
	err := owner.WithMCPToolAuthority(context.Background(), token, func() error {
		clientCalls++
		return nil
	})
	if err == nil || clientCalls != 0 {
		t.Fatalf("stale call error=%v client calls=%d, want rejection/0", err, clientCalls)
	}
}

func assertTask4BExternalAuthorityGenerations(
	t *testing.T,
	store *memoryMCPServerStore,
	owner contract.MCPToolAuthorityOwner,
	cwd string,
	binary providerdto.MCPBinary,
) (contract.MCPToolAuthority, contract.MCPToolAuthority) {
	t.Helper()
	first := issueTask4BExternalAuthority(t, owner, cwd, binary, "membership-v1")
	second := issueTask4BExternalAuthority(t, owner, cwd, binary, "membership-v2")
	if first.Generation != 1 || second.Generation != 2 || first.ConfigDigest != second.ConfigDigest {
		t.Fatalf("authority generations/digests = %#v / %#v", first, second)
	}
	if err := owner.CheckMCPToolAuthority(context.Background(), first); err == nil {
		t.Fatal("superseded membership authority remained current")
	}

	store.seed(cwd, binary.Name, ServerConfig{Transport: "http", URL: "https://example.test/v2"})
	if err := owner.CheckMCPToolAuthority(context.Background(), second); err == nil {
		t.Fatal("authority remained current after config digest changed")
	}
	binary.URL = "https://example.test/v2"
	third := issueTask4BExternalAuthority(t, owner, cwd, binary, "membership-v3")
	if third.Generation != 3 || third.ConfigDigest == second.ConfigDigest {
		t.Fatalf("replacement authority = %#v, want generation 3 with changed config digest", third)
	}
	return second, third
}

func assertTask4BExternalAuthorityCAS(
	t *testing.T,
	owner contract.MCPToolAuthorityOwner,
	stale contract.MCPToolAuthority,
	current contract.MCPToolAuthority,
) {
	t.Helper()
	publishCount := 0
	commit := func(token contract.MCPToolAuthority) error {
		return owner.CompareAndSwapMCPToolQuarantines(context.Background(), []contract.MCPToolQuarantineCommit{{
			Authority: token,
			Tools:     map[string]string{"bad": "schema"},
		}}, func() error {
			publishCount++
			return nil
		})
	}
	if err := commit(stale); err == nil || publishCount != 0 {
		t.Fatalf("stale CAS error/publish count = %v/%d, want failure/0", err, publishCount)
	}
	if err := commit(current); err != nil || publishCount != 1 {
		t.Fatalf("current CAS error/publish count = %v/%d, want nil/1", err, publishCount)
	}
	state := owner.(*mcpToolAuthorityOwner).current[mcpToolAuthorityKey(current)]
	if state.quarantine["bad"] != "schema" || len(state.quarantine) != 1 {
		t.Fatalf("current quarantine = %#v, want bad=schema", state.quarantine)
	}
}

func issueTask4BExternalAuthority(
	t *testing.T,
	owner contract.MCPToolAuthorityOwner,
	cwd string,
	binary providerdto.MCPBinary,
	membership string,
) contract.MCPToolAuthority {
	t.Helper()
	token, err := owner.IssueMCPToolAuthority(context.Background(), contract.MCPToolAuthorityIssueRequest{
		CWD: cwd, Binary: binary, MembershipDigest: membership,
	})
	if err != nil {
		t.Fatalf("IssueMCPToolAuthority() error = %v", err)
	}
	return token
}
