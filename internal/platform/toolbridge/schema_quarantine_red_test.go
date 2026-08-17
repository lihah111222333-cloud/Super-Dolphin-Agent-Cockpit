package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/schema"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

// TestTask4BToolsListRawDecodeParity 锁定 HTTP/stdio 共用 decode 只校验 tools 数组，
// 单项 schema 语义留给 identity/classification 后的 admission 处理。
func TestTask4BToolsListRawDecodeParity(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"tools":[{"name":"good","inputSchema":{"type":"object","properties":{"q":{"type":"string"}}}},{"name":"bad","inputSchema":true}]}`)
	for _, source := range []string{"HTTP MCP tools/list", "stdio MCP tools/list"} {
		t.Run(source, func(t *testing.T) {
			tools, err := decodePeerToolsListResult(raw, source)
			if err != nil {
				t.Fatalf("decodePeerToolsListResult() error = %v", err)
			}
			if len(tools) != 2 {
				t.Fatalf("len(tools) = %d, want 2", len(tools))
			}
			if got := tools[1].InputSchema; !bytes.Equal(got, json.RawMessage(`true`)) {
				t.Fatalf("bad input schema = %s, want raw true", got)
			}
			if got := tools[0].RawJSON(); !bytes.Contains(got, []byte(`"name":"good"`)) {
				t.Fatalf("good raw tool = %s, want original object", got)
			}
			if got := tools[1].RawJSON(); !bytes.Contains(got, []byte(`"inputSchema":true`)) {
				t.Fatalf("bad raw tool = %s, want original object", got)
			}
		})
	}
}

func TestTask4BToolsListStillRejectsNonArray(t *testing.T) {
	t.Parallel()

	if _, err := decodePeerToolsListResult(json.RawMessage(`{"tools":{}}`), "HTTP MCP tools/list"); err == nil {
		t.Fatal("decodePeerToolsListResult() error = nil, want tools array rejection")
	}
}

type task4BAuthorityOwner struct {
	mu             sync.Mutex
	generation     uint64
	current        map[string]contract.MCPToolAuthority
	quarantine     map[string]map[string]string
	checkCount     int
	staleAtCheck   int
	staleBeforeCAS bool
	publishCount   int
}

func newTask4BAuthorityOwner() *task4BAuthorityOwner {
	return &task4BAuthorityOwner{
		current:    map[string]contract.MCPToolAuthority{},
		quarantine: map[string]map[string]string{},
	}
}

func (o *task4BAuthorityOwner) IssueMCPToolAuthority(
	_ context.Context,
	req contract.MCPToolAuthorityIssueRequest,
) (contract.MCPToolAuthority, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	managed := req.Binary.IsManagedMCPBinary()
	if !managed && req.Binary.TrustedServerID != req.Binary.Name {
		return contract.MCPToolAuthority{}, errors.New("test config-owner identity rejected")
	}
	o.generation++
	token := contract.MCPToolAuthority{
		CWD:              req.CWD,
		ServerID:         req.Binary.Name,
		ConfigDigest:     "test-config-digest",
		MembershipDigest: req.MembershipDigest,
		Generation:       o.generation,
		Managed:          managed,
	}
	o.current[token.ServerID] = token
	return token, nil
}

func (o *task4BAuthorityOwner) CheckMCPToolAuthority(_ context.Context, token contract.MCPToolAuthority) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.checkCount++
	if o.staleAtCheck > 0 && o.checkCount == o.staleAtCheck {
		o.invalidateLocked(token.ServerID)
	}
	if current, ok := o.current[token.ServerID]; !ok || current != token {
		return errors.New("test authority stale")
	}
	return nil
}

func (o *task4BAuthorityOwner) WithMCPToolAuthority(_ context.Context, token contract.MCPToolAuthority, call func() error) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if current, ok := o.current[token.ServerID]; !ok || current != token {
		return errors.New("test authority stale before call")
	}
	return call()
}

func (o *task4BAuthorityOwner) CompareAndSwapMCPToolQuarantines(
	_ context.Context,
	commits []contract.MCPToolQuarantineCommit,
	publish func() error,
) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.staleBeforeCAS && len(commits) > 0 {
		o.staleBeforeCAS = false
		o.invalidateLocked(commits[0].Authority.ServerID)
	}
	for _, commit := range commits {
		if o.current[commit.Authority.ServerID] != commit.Authority {
			return errors.New("test authority stale before publish")
		}
	}
	if err := publish(); err != nil {
		return err
	}
	o.publishCount++
	for _, commit := range commits {
		o.quarantine[commit.Authority.ServerID] = cloneTask4BStrings(commit.Tools)
	}
	return nil
}

func (o *task4BAuthorityOwner) invalidate(serverID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.invalidateLocked(serverID)
}

func (o *task4BAuthorityOwner) invalidateLocked(serverID string) {
	current := o.current[serverID]
	o.generation++
	current.Generation = o.generation
	o.current[serverID] = current
}

func (o *task4BAuthorityOwner) quarantineFor(serverID string) map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return cloneTask4BStrings(o.quarantine[serverID])
}

func cloneTask4BStrings(source map[string]string) map[string]string {
	out := make(map[string]string, len(source))
	maps.Copy(out, source)
	return out
}

type task4BSchemaExecutor struct {
	mu        sync.Mutex
	failCodes map[string]schema.Code
	failText  string
	calls     []schema.Invocation
}

func (e *task4BSchemaExecutor) Execute(
	ctx context.Context,
	invocation schema.Invocation,
	fence schema.FenceHook,
) (schema.Result, error) {
	e.mu.Lock()
	e.calls = append(e.calls, invocation)
	code := e.failCodes[invocation.ToolName]
	message := e.failText
	e.mu.Unlock()
	identity := schema.FenceIdentity{
		ServerID:            invocation.ServerID,
		ToolName:            invocation.ToolName,
		AuthorityGeneration: invocation.AuthorityGeneration,
		SchemaDigest:        invocation.Schema.Digest,
	}
	if err := fence(ctx, schema.FenceBeforeLaunch, identity); err != nil {
		return schema.Result{}, err
	}
	if code != "" {
		if message == "" {
			message = "task4b injected failure"
		}
		return schema.Result{}, &schema.Diagnostic{Code: code, Message: message}
	}
	if err := validateTask4BArguments(invocation); err != nil {
		return schema.Result{}, err
	}
	if err := fence(ctx, schema.FenceAfterSuccess, identity); err != nil {
		return schema.Result{}, err
	}
	return schema.Result{Operation: invocation.Operation, SchemaDigest: invocation.Schema.Digest, ArgumentsValid: true}, nil
}

func validateTask4BArguments(invocation schema.Invocation) error {
	if invocation.Operation != schema.OperationValidate {
		return nil
	}
	var document struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties *bool                      `json:"additionalProperties"`
	}
	if err := json.Unmarshal(invocation.Schema.Bytes, &document); err != nil {
		return err
	}
	if document.AdditionalProperties == nil || *document.AdditionalProperties {
		return nil
	}
	var arguments map[string]json.RawMessage
	if err := json.Unmarshal(invocation.Arguments, &arguments); err != nil {
		return err
	}
	for name := range arguments {
		if _, ok := document.Properties[name]; !ok {
			return &schema.Diagnostic{Code: schema.CodeArgumentInvalid, Message: fmt.Sprintf("argument %q is not allowed", name)}
		}
	}
	return nil
}

func (e *task4BSchemaExecutor) operationCount(operation schema.Operation) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	count := 0
	for _, invocation := range e.calls {
		if invocation.Operation == operation {
			count++
		}
	}
	return count
}

type task4BMCPClient struct {
	mu     sync.Mutex
	tools  []mcpdto.MCPTool
	calls  int
	closed int
}

func (c *task4BMCPClient) ListTools(context.Context) ([]mcpdto.MCPTool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]mcpdto.MCPTool(nil), c.tools...), nil
}

func (c *task4BMCPClient) CallTool(context.Context, string, json.RawMessage, ToolCallRequest) (*ToolCallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return toolCallTextResult(true, "ok"), nil
}

func (c *task4BMCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed++
	return nil
}

func (c *task4BMCPClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *task4BMCPClient) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func task4BExternalBinary(name string) providerdto.MCPBinary {
	return providerdto.MCPBinary{Name: name, TrustedServerID: name, Command: []string{"/test/" + name}}
}

func task4BTool(name, inputSchema string) mcpdto.MCPTool {
	return mcpdto.NewRawTool(json.RawMessage(fmt.Sprintf(`{"name":%q,"description":%q,"inputSchema":%s}`, name, name, inputSchema)))
}

func task4BScope(binary providerdto.MCPBinary) contract.CodexToolSurfaceScope {
	return contract.CodexToolSurfaceScope{
		AgentID:          "task4b-agent",
		ProviderThreadID: "task4b-thread",
		CWD:              portableToolbridgeTestCWD("task4b", "repo"),
		Manifest:         providerdto.MCPManifest{Binaries: []providerdto.MCPBinary{binary}},
	}
}

func task4BHandler(owner *task4BAuthorityOwner, executor *task4BSchemaExecutor, client *task4BMCPClient) *Handler {
	return &Handler{
		authorityOwner: owner,
		schemaExecutor: executor,
		stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
			return client, nil
		},
	}
}

func TestTask4BRawIdentityRejectsAmbiguousObjects(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"duplicate name":    `{"name":"first","name":"last","inputSchema":{"type":"object"}}`,
		"unknown field":     `{"name":"tool","extra":true,"inputSchema":{"type":"object"}}`,
		"missing name":      `{"inputSchema":{"type":"object"}}`,
		"non-string name":   `{"name":42,"inputSchema":{"type":"object"}}`,
		"missing schema":    `{"name":"tool"}`,
		"duplicate schema":  `{"name":"tool","inputSchema":true,"inputSchema":{"type":"object"}}`,
		"unknown execution": `{"name":"tool","inputSchema":{"type":"object"},"execution":{"extra":true}}`,
		"duplicate support": `{"name":"tool","inputSchema":{"type":"object"},"execution":{"taskSupport":"optional","taskSupport":"required"}}`,
		"invalid support":   `{"name":"tool","inputSchema":{"type":"object"},"execution":{"taskSupport":"sometimes"}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, _, _, err := mcpToolMembership([]mcpdto.MCPTool{mcpdto.NewRawTool(json.RawMessage(raw))})
			if err == nil {
				t.Fatal("mcpToolMembership() error = nil, want ambiguous raw identity rejection")
			}
		})
	}
}

func TestTask4BMixedTrustedExternalQuarantinesOnlyBadTool(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{
		task4BTool("good", `{"type":"object","properties":{"q":{"type":"string"}}}`),
		task4BTool("bad", `true`),
	}}
	binary := task4BExternalBinary("external")
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(binary))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if len(tools) != 1 || tools[0].Name == "bad" {
		t.Fatalf("dynamic tools = %+v, want only good", tools)
	}
	if got := owner.quarantineFor(binary.Name); got["bad"] != string(schema.CodeRootNotObject) || len(got) != 1 {
		t.Fatalf("quarantine = %#v, want only bad root-not-object", got)
	}
	surface := h.lookupCodexToolSurface(ToolCallRequest{AgentID: "task4b-agent"})
	if surface == nil {
		t.Fatal("published surface = nil")
	}
	if _, err := h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: "bad", Arguments: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("bad quarantined tool call error = nil")
	}
	if executor.operationCount(schema.OperationValidate) != 0 || client.callCount() != 0 {
		t.Fatal("bad quarantined tool reached validator or client")
	}
}

func TestTask4BMixedManagedFailsFastAndWithdrawsOldSurface(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{
		task4BTool("good", `{"type":"object"}`),
		task4BTool("bad", `true`),
	}}
	binary := providerdto.NewManagedMCPBinary(providerdto.MCPBinary{Name: "lsp", Command: []string{"/test/mcp-lsp"}})
	scope := task4BScope(binary)
	h := task4BHandler(owner, executor, client)
	oldClient := &task4BMCPClient{}
	oldSurface := &codexToolSurface{keys: codexSurfaceKeys(scope), clients: []mcpClient{oldClient}}
	h.surfaces = map[string]*codexToolSurface{}
	for _, key := range oldSurface.keys {
		h.surfaces[key] = oldSurface
	}
	if _, err := h.PrepareCodexToolSurface(context.Background(), scope); err == nil {
		t.Fatal("PrepareCodexToolSurface() error = nil, want managed fail-fast")
	}
	if h.lookupCodexToolSurface(ToolCallRequest{AgentID: scope.AgentID}) != nil {
		t.Fatal("managed failure left old or partial surface published")
	}
	if owner.publishCount != 0 || len(owner.quarantineFor(binary.Name)) != 0 {
		t.Fatal("managed failure committed publish or quarantine")
	}
	if oldClient.closed != 1 || client.closed != 1 {
		t.Fatalf("closed old/new clients = %d/%d, want 1/1", oldClient.closed, client.closed)
	}
}

func TestTask4BAuthorityStaleBeforeCompileAfterSuccessAndPublish(t *testing.T) {
	cases := []struct {
		name           string
		staleAtCheck   int
		staleBeforeCAS bool
	}{
		{name: "before compile", staleAtCheck: 1},
		{name: "after success", staleAtCheck: 2},
		{name: "before publish", staleBeforeCAS: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := newTask4BAuthorityOwner()
			owner.staleAtCheck = tc.staleAtCheck
			owner.staleBeforeCAS = tc.staleBeforeCAS
			executor := &task4BSchemaExecutor{}
			client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("good", `{"type":"object"}`)}}
			h := task4BHandler(owner, executor, client)
			if _, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external"))); err == nil {
				t.Fatal("PrepareCodexToolSurface() error = nil, want stale rejection")
			}
			if h.lookupCodexToolSurface(ToolCallRequest{AgentID: "task4b-agent"}) != nil {
				t.Fatal("stale generation published a surface")
			}
			if owner.publishCount != 0 || len(owner.quarantineFor("external")) != 0 || client.callCount() != 0 {
				t.Fatal("stale generation produced publish, quarantine, or client call side effect")
			}
		})
	}
}

func TestTask4BAuthorityStaleBeforeCallSkipsValidatorAndClient(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("good", `{"type":"object"}`)}}
	binary := task4BExternalBinary("external")
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(binary))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	owner.invalidate(binary.Name)
	surface := h.lookupCodexToolSurface(ToolCallRequest{AgentID: "task4b-agent"})
	if _, err := h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: tools[0].Name, Arguments: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("callCodexSurfaceTool() error = nil, want stale rejection")
	}
	if executor.operationCount(schema.OperationValidate) != 0 || client.callCount() != 0 {
		t.Fatal("stale call reached validator or client")
	}
}

func TestTask4BSchemaAdmissionErrorRedactsRecoverySecrets(t *testing.T) {
	secret := task4BRecoverySecret()
	authority := &mcpSchemaAuthority{token: contract.MCPToolAuthority{ServerID: "external"}}
	err := handleMCPAdmissionError(
		authority, "unsafe", &schema.Diagnostic{Code: schema.CodeProtocolViolation, Message: secret}, map[string]string{},
	)
	assertTask4BSafeRecoveryError(t, err, schema.CodeProtocolViolation)
}

func TestTask4BSchemaRuntimeValidationFailureReachesProviderAsStructuredResult(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("unsafe", `{"type":"object"}`)}}
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	executor.mu.Lock()
	executor.failCodes = map[string]schema.Code{"unsafe": schema.CodeReapFailed}
	executor.failText = task4BRecoverySecret()
	executor.mu.Unlock()
	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name": tools[0].Name, "arguments": json.RawMessage(`{}`), "agentId": "task4b-agent", "threadId": "task4b-thread",
	})})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v, want provider-visible recovery result", err)
	}
	assertTask4BProviderRecoveryResult(t, result, schema.CodeReapFailed)
	if client.callCount() != 0 {
		t.Fatalf("unsafe schema call reached MCP client %d times", client.callCount())
	}
}

func TestTask4BUnknownRuntimeValidationFailureStillReturnsError(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("unsafe", `{"type":"object"}`)}}
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	executor.mu.Lock()
	executor.failCodes = map[string]schema.Code{"unsafe": schema.CodeArgumentInvalid}
	executor.mu.Unlock()
	result, err := h.HandleToolCall(context.Background(), contract.ToolCallRawMessage{Params: mustRawJSON(t, map[string]any{
		"name": tools[0].Name, "arguments": json.RawMessage(`{}`), "agentId": "task4b-agent", "threadId": "task4b-thread",
	})})
	got, _ := result.(*ToolCallResult)
	if err == nil || got != nil {
		t.Fatalf("HandleToolCall() = %#v, %v, want unknown error to fail fast", result, err)
	}
}

func assertTask4BProviderRecoveryResult(t *testing.T, result any, code schema.Code) {
	t.Helper()
	got, ok := result.(*ToolCallResult)
	if !ok || got.Success || len(got.ContentItems) != 1 || got.ContentItems[0].Text != "Recovery action is required. Sensitive diagnostics remain preserved internally." {
		t.Fatalf("HandleToolCall() result = %#v, want fixed safe failure", result)
	}
	var failure contract.RecoveryFailure
	if err := json.Unmarshal(got.StructuredContent, &failure); err != nil {
		t.Fatalf("decode structuredContent %q: %v", got.StructuredContent, err)
	}
	want, _ := contract.RecoveryFailureForCode(string(code), "")
	if failure != want {
		t.Fatalf("structuredContent = %#v, want %#v", failure, want)
	}
	var fields map[string]any
	if err := json.Unmarshal(got.StructuredContent, &fields); err != nil || len(fields) != 4 {
		t.Fatalf("structuredContent fields = %#v, err=%v, want exactly four", fields, err)
	}
}

func task4BRecoverySecret() string {
	return "stdout=/Users/alice/private.db stderr=postgres://admin:password@localhost/db PRIVATE KEY sk-live-secret"
}

func assertTask4BSafeRecoveryError(t *testing.T, err error, code schema.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("schema boundary error = nil")
	}
	failure, ok := schema.RecoveryFailure(err)
	if !ok || failure.Code != string(code) {
		t.Fatalf("RecoveryFailure() = %#v, %v; want code %q", failure, ok, code)
	}
	for _, secret := range []string{"stdout=", "stderr=", "postgres://", "PRIVATE KEY", "sk-live-secret", "/Users/alice"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("schema boundary error leaked %q in %q", secret, err)
		}
	}
}

func TestTask4BRepairThenRebreakUpdatesSurfaceAndQuarantine(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	binary := task4BExternalBinary("external")
	current := []mcpdto.MCPTool{task4BTool("repairable", `true`)}
	h := &Handler{authorityOwner: owner, schemaExecutor: executor}
	h.stdioClientFactory = func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
		return &task4BMCPClient{tools: append([]mcpdto.MCPTool(nil), current...)}, nil
	}
	prepare := func() []contract.DynamicToolSchema {
		tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(binary))
		if err != nil {
			t.Fatalf("PrepareCodexToolSurface() error = %v", err)
		}
		return tools
	}
	if tools := prepare(); len(tools) != 0 || len(owner.quarantineFor(binary.Name)) != 1 {
		t.Fatalf("initial broken tools/quarantine = %+v/%#v", tools, owner.quarantineFor(binary.Name))
	}
	current = []mcpdto.MCPTool{task4BTool("repairable", `{"type":"object"}`)}
	if tools := prepare(); len(tools) != 1 || len(owner.quarantineFor(binary.Name)) != 0 {
		t.Fatalf("repaired tools/quarantine = %+v/%#v", tools, owner.quarantineFor(binary.Name))
	}
	current = []mcpdto.MCPTool{task4BTool("repairable", `true`)}
	if tools := prepare(); len(tools) != 0 || len(owner.quarantineFor(binary.Name)) != 1 {
		t.Fatalf("rebroken tools/quarantine = %+v/%#v", tools, owner.quarantineFor(binary.Name))
	}
}

func TestMCP20251125ToolWireFieldGuard(t *testing.T) {
	got, err := mcpToolWireFieldSet()
	if err != nil {
		t.Fatalf("mcpToolWireFieldSet() error = %v", err)
	}
	want := map[string]struct{}{
		"name": {}, "title": {}, "description": {}, "icons": {},
		"inputSchema": {}, "outputSchema": {}, "annotations": {},
		"_meta": {}, "execution": {},
	}
	if !maps.Equal(got, want) {
		t.Fatalf("MCP Tool wire fields = %#v, want %#v", got, want)
	}
}

func TestMCP20251125ToolFieldsAdmitAndTaskSupportFailsClosed(t *testing.T) {
	legalTool := func(taskSupport string) mcpdto.MCPTool {
		raw := fmt.Sprintf(`{"name":"modern","title":"Modern Tool","description":"modern fixture","icons":[{"src":"https://example.test/icon.png","mimeType":"image/png","sizes":["48x48"]}],"inputSchema":{"type":"object","additionalProperties":false},"outputSchema":{"type":"object"},"annotations":{"title":"Modern annotation","readOnlyHint":true,"destructiveHint":false,"idempotentHint":true,"openWorldHint":false},"_meta":{"com.example/cache-key":"v1"},"execution":{"taskSupport":%q}}`, taskSupport)
		return mcpdto.NewRawTool(json.RawMessage(raw))
	}
	for _, taskSupport := range []string{"forbidden", "optional"} {
		t.Run(taskSupport, func(t *testing.T) {
			owner := newTask4BAuthorityOwner()
			executor := &task4BSchemaExecutor{}
			client := &task4BMCPClient{tools: []mcpdto.MCPTool{legalTool(taskSupport)}}
			h := task4BHandler(owner, executor, client)
			tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
			if err != nil {
				t.Fatalf("PrepareCodexToolSurface() error = %v", err)
			}
			surface := h.lookupCodexToolSurface(ToolCallRequest{ThreadID: "task4b-thread"})
			if _, err := h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: tools[0].Name, Arguments: json.RawMessage(`{}`)}); err != nil {
				t.Fatalf("callCodexSurfaceTool() error = %v", err)
			}
			if client.callCount() != 1 {
				t.Fatalf("client calls = %d, want normal call", client.callCount())
			}
		})
	}
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{legalTool("required")}}
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(required) error = %v", err)
	}
	surface := h.lookupCodexToolSurface(ToolCallRequest{ThreadID: "task4b-thread"})
	_, err = h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: tools[0].Name, Arguments: json.RawMessage(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "task augmentation") {
		t.Fatalf("callCodexSurfaceTool(required) error = %v, want task augmentation fail-closed", err)
	}
	if client.callCount() != 0 {
		t.Fatalf("required client calls = %d, want 0", client.callCount())
	}
}

func TestMCP20251125ExecutionChangesMembershipButMetadataDoesNotAuthorize(t *testing.T) {
	tool := func(execution, annotation string) mcpdto.MCPTool {
		raw := fmt.Sprintf(`{"name":"semantic","inputSchema":{"type":"object"},"annotations":{"title":%q},"_meta":{"com.example/note":%q},"execution":{"taskSupport":%q}}`, annotation, annotation, execution)
		return mcpdto.NewRawTool(json.RawMessage(raw))
	}
	_, _, forbiddenDigest, err := mcpToolMembership([]mcpdto.MCPTool{tool("forbidden", "first")})
	if err != nil {
		t.Fatalf("mcpToolMembership(forbidden) error = %v", err)
	}
	_, _, optionalDigest, err := mcpToolMembership([]mcpdto.MCPTool{tool("optional", "first")})
	if err != nil {
		t.Fatalf("mcpToolMembership(optional) error = %v", err)
	}
	_, _, metadataDigest, err := mcpToolMembership([]mcpdto.MCPTool{tool("forbidden", "changed")})
	if err != nil {
		t.Fatalf("mcpToolMembership(metadata) error = %v", err)
	}
	if forbiddenDigest == optionalDigest {
		t.Fatal("execution.taskSupport mutation did not change membership identity")
	}
	if forbiddenDigest != metadataDigest {
		t.Fatal("annotations/_meta changed authority identity")
	}
}

type task4BPrepareBarrierExecutor struct {
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
	inner   task4BSchemaExecutor
}

func (e *task4BPrepareBarrierExecutor) Execute(
	ctx context.Context,
	invocation schema.Invocation,
	fence schema.FenceHook,
) (schema.Result, error) {
	e.mu.Lock()
	e.calls++
	first := e.calls == 1
	e.mu.Unlock()
	if first {
		close(e.started)
		select {
		case <-e.release:
		case <-ctx.Done():
			return schema.Result{}, ctx.Err()
		}
	}
	return e.inner.Execute(ctx, invocation, fence)
}

func TestStalePrepareFailureDoesNotRevokeNewGenerationSurface(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BPrepareBarrierExecutor{started: make(chan struct{}), release: make(chan struct{})}
	clientA := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("tool_a", `{"type":"object"}`)}}
	clientB := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("tool_b", `{"type":"object"}`)}}
	clients := []mcpClient{clientA, clientB}
	var factoryMu sync.Mutex
	h := &Handler{
		authorityOwner: owner,
		schemaExecutor: executor,
		stdioClientFactory: func(context.Context, providerdto.MCPBinary) (mcpClient, error) {
			factoryMu.Lock()
			defer factoryMu.Unlock()
			client := clients[0]
			clients = clients[1:]
			return client, nil
		},
	}
	scope := task4BScope(task4BExternalBinary("external"))
	errA := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.stale-prepare-a", func(context.Context) {
		_, err := h.PrepareCodexToolSurface(context.Background(), scope)
		errA <- err
	})
	<-executor.started
	toolsB, err := h.PrepareCodexToolSurface(context.Background(), scope)
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface(B) error = %v", err)
	}
	close(executor.release)
	if err := <-errA; err == nil {
		t.Fatal("PrepareCodexToolSurface(A) error = nil, want stale authority failure")
	}
	surface := h.lookupCodexToolSurface(ToolCallRequest{ThreadID: "task4b-thread"})
	if surface == nil {
		t.Fatal("new generation surface was revoked by stale prepare cleanup")
	}
	if clientB.closeCount() != 0 {
		t.Fatalf("new generation client close count = %d, want 0", clientB.closeCount())
	}
	if _, err := h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: toolsB[0].Name, Arguments: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("new generation tool call error = %v", err)
	}
	if clientB.callCount() != 1 {
		t.Fatalf("new generation client calls = %d, want 1", clientB.callCount())
	}
	if clientA.closeCount() != 1 {
		t.Fatalf("stale local client close count = %d, want 1", clientA.closeCount())
	}
}

func TestTask4BHelperCancelAndOversizeHaveNoQuarantineSideEffect(t *testing.T) {
	for _, code := range []schema.Code{schema.CodeCancelled, schema.CodeOutputTooLarge} {
		t.Run(string(code), func(t *testing.T) {
			owner := newTask4BAuthorityOwner()
			executor := &task4BSchemaExecutor{failCodes: map[string]schema.Code{"good": code}}
			client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("good", `{"type":"object"}`)}}
			h := task4BHandler(owner, executor, client)
			if _, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external"))); err == nil {
				t.Fatal("PrepareCodexToolSurface() error = nil, want helper failure")
			}
			if owner.publishCount != 0 || len(owner.quarantineFor("external")) != 0 {
				t.Fatal("helper infrastructure failure published surface or quarantine")
			}
		})
	}
}

func TestTask4BSchemaExecutorConcurrentCallsAreRaceFree(t *testing.T) {
	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool("good", `{"type":"object"}`)}}
	h := task4BHandler(owner, executor, client)
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	surface := h.lookupCodexToolSurface(ToolCallRequest{AgentID: "task4b-agent"})
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		safego.Go(context.Background(), nil, "toolbridge.task4b.concurrent-call-test", func(context.Context) {
			defer wg.Done()
			if _, callErr := h.callCodexSurfaceTool(context.Background(), surface, ToolCallRequest{Name: tools[0].Name, Arguments: json.RawMessage(`{}`)}); callErr != nil {
				t.Errorf("callCodexSurfaceTool() error = %v", callErr)
			}
		})
	}
	wg.Wait()
	if client.callCount() != 16 || executor.operationCount(schema.OperationValidate) != 16 {
		t.Fatalf("client/validator calls = %d/%d, want 16/16", client.callCount(), executor.operationCount(schema.OperationValidate))
	}
}
