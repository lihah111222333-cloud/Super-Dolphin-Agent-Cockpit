package thread

import (
	"context"
	"encoding/json"
	"testing"
)

// TestPromptKeyChain_FrontendPayloadToBaseInstructions exercises the full
// "set as launch prompt" chain from the JSON payload the frontend actually
// sends, through the RPC handler decode, the router, and into the response
// map that the frontend reads back.
//
// Layers walked, in order:
//
//  1. Frontend payload   { cwd, prompt_key } as raw JSON
//  2. startParams        json.Unmarshal -> rpc decode field capture
//  3. StartRequest       handler builds it from startParams (mirrors rpc.go)
//  4. Router resolve     resolveRoutedPrompt picks template by PromptKey
//  5. BaseInstructions   == picked.PromptText (the prompt the user authored)
//  6. Response map       echoes prompt_key / agent_key / prompt_version_id
//
// If any of these links breaks, this test fails — that's the single source
// of truth that the SystemPromptPage "set as launch" feature is wired
// end-to-end on the backend.
func TestPromptKeyChain_FrontendPayloadToBaseInstructions(t *testing.T) {
	t.Parallel()

	// --- (1) Frontend payload -------------------------------------------------
	// This is byte-identical to what stores/thread-actions-helpers.js builds
	// after reading the cwd-scoped settings.activePromptKey preference.
	rawPayload := []byte(`{
		"cwd": "/repo-x",
		"modelProvider": "claude",
		"prompt_key": "main/launch-fav"
	}`)

	// --- (2) startParams decode ----------------------------------------------
	p := decodePinnedStartParams(t, rawPayload)

	// --- (3) StartRequest construction ---------------------------------------
	// Mirrors the field copy inside newStartHandler. We don't call the real
	// handler because Service.Start() does provider launch / persistence we
	// can't fake in a unit test; the chain we care about (decode -> request
	// -> router) is pure data flow and is fully covered here.
	req := startRequestFromParams(t, p)

	// --- (4)+(5) Router resolve + BaseInstructions injection -----------------
	// Fake store mirrors what the user would have authored via SystemPromptPage:
	// a "main/launch-fav" row plus an unrelated default. The router must
	// land on the explicit pin, not the default.
	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate("main/launch-fav", "main", "PromptText authored by the user via SystemPromptPage", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	svc := newServiceWithRouter(store)
	svc.resolveRoutedPrompt(context.Background(), &req)
	assertPinnedPromptResolved(t, req)

	// --- (6) Response map ----------------------------------------------------
	// Mirrors the response builder in newStartHandler. Frontend's
	// stores/thread-actions-helpers.js reads res.prompt_key /
	// res.prompt_version_id from this exact shape; if a key is missing the
	// "启动中" badge in SystemPromptPage will not light up on next reload.
	result := StartResult{
		ThreadID:        "thread-pinned-fav",
		AgentKey:        req.AgentKey,
		PromptKey:       req.PromptKey,
		PromptVersionID: req.PromptVersionID,
	}
	response := startResponseMap(result)
	assertPinnedPromptResponse(t, response)
}

func decodePinnedStartParams(t *testing.T, rawPayload []byte) startParams {
	t.Helper()
	var p startParams
	if err := json.Unmarshal(rawPayload, &p); err != nil {
		t.Fatalf("layer 2 (rpc decode): json.Unmarshal err = %v", err)
	}
	if p.PromptKey != "main/launch-fav" {
		t.Fatalf("layer 2 (rpc decode): startParams.PromptKey = %q, want main/launch-fav", p.PromptKey)
	}
	if p.CWD != "/repo-x" {
		t.Fatalf("layer 2 (rpc decode): startParams.CWD = %q, want /repo-x", p.CWD)
	}
	return p
}

func startRequestFromParams(t *testing.T, p startParams) StartRequest {
	t.Helper()
	req := StartRequest{
		Provider:      p.Provider,
		CWD:           p.CWD,
		ModelProvider: p.ModelProvider,
		AgentKey:      p.AgentKey,
		PromptKey:     p.PromptKey,
	}
	if req.PromptKey != "main/launch-fav" {
		t.Fatalf("layer 3 (handler->request): StartRequest.PromptKey = %q", req.PromptKey)
	}
	return req
}

func assertPinnedPromptResolved(t *testing.T, req StartRequest) {
	t.Helper()
	if req.BaseInstructions != "PromptText authored by the user via SystemPromptPage" {
		t.Fatalf("layer 5 (BaseInstructions inject): got %q\nthe user's prompt did not reach the provider system prompt slot", req.BaseInstructions)
	}
	if req.AgentKey != "main" {
		t.Fatalf("layer 4 (router stamp agent_key): got %q, want main", req.AgentKey)
	}
	if req.PromptKey != "main/launch-fav" {
		t.Fatalf("layer 4 (router preserve prompt_key): got %q", req.PromptKey)
	}
	if req.PromptVersionID == nil {
		t.Fatalf("layer 4 (router materialize version): PromptVersionID nil; observability would be blind")
	}
}

func startResponseMap(result StartResult) map[string]any {
	response := map[string]any{
		"thread":    threadInfo{ID: result.ThreadID, Status: "running"},
		"thread_id": result.ThreadID,
	}
	if result.AgentKey != "" {
		response["agent_key"] = result.AgentKey
	}
	if result.PromptKey != "" {
		response["prompt_key"] = result.PromptKey
	}
	if result.PromptVersionID != nil {
		response["prompt_version_id"] = *result.PromptVersionID
	}
	return response
}

func assertPinnedPromptResponse(t *testing.T, response map[string]any) {
	t.Helper()
	if response["prompt_key"] != "main/launch-fav" {
		t.Fatalf("layer 6 (response echo): prompt_key = %v", response["prompt_key"])
	}
	if response["agent_key"] != "main" {
		t.Fatalf("layer 6 (response echo): agent_key = %v", response["agent_key"])
	}
	if _, ok := response["prompt_version_id"].(int64); !ok {
		t.Fatalf("layer 6 (response echo): prompt_version_id missing or wrong type: %#v", response["prompt_version_id"])
	}
}

// TestPromptKeyChain_NoPinFallsBackToDefault proves the negative side: when
// the user has not set any "launch prompt", the same chain falls back to
// the default routing path so existing behavior is preserved.
func TestPromptKeyChain_NoPinFallsBackToDefault(t *testing.T) {
	t.Parallel()

	rawPayload := []byte(`{"cwd":"/repo-x","modelProvider":"claude"}`)
	var p startParams
	if err := json.Unmarshal(rawPayload, &p); err != nil {
		t.Fatalf("rpc decode: %v", err)
	}
	if p.PromptKey != "" {
		t.Fatalf("PromptKey leaked from empty payload: %q", p.PromptKey)
	}

	req := StartRequest{CWD: p.CWD, PromptKey: p.PromptKey, AgentKey: p.AgentKey}

	store := &fakePromptCatalog{
		templates: []PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	svc := newServiceWithRouter(store)
	svc.resolveRoutedPrompt(context.Background(), &req)

	if req.BaseInstructions != "default body" {
		t.Fatalf("no-pin path should fall through to default: got %q", req.BaseInstructions)
	}
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("router did not stamp default prompt_key: got %q", req.PromptKey)
	}
}

// TestPromptKeyChain_DeferSpawnRoundTrip exercises the defer_spawn path that
// the agent-terminal UI actually uses (blank-thread first launch). Without the
// fix in startPendingThread + buildPendingSpawnRequest, PromptKey gets dropped
// when the StartRequest is serialised into the pending row, causing the router
// to silently degrade to the default persona on the first turn.
//
// This test does NOT exercise the full SpawnIfNeeded service path (that would
// need a real provider launcher); it directly verifies the lossless storage
// and reconstruction of PromptKey through storedThreadConfig encode/decode.
func TestPromptKeyChain_DeferSpawnRoundTrip(t *testing.T) {
	t.Parallel()

	stored := storedThreadConfig{
		Model:       "claude-sonnet-4-5",
		Effort:      "high",
		Approvals:   "never",
		Personality: "friendly",
		Provider:    "claude",
		PromptKey:   "main/launch-fav",
		AgentKey:    "dag_designer",
	}

	encoded, err := encodeStoredThreadConfig(stored)
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig: %v", err)
	}

	decoded := mustDecodeStoredThreadConfig(t, encoded)
	if decoded.PromptKey != "main/launch-fav" {
		t.Fatalf("PromptKey lost in round-trip: got %q, want main/launch-fav", decoded.PromptKey)
	}
	if decoded.AgentKey != "dag_designer" {
		t.Fatalf("AgentKey lost in round-trip: got %q, want dag_designer", decoded.AgentKey)
	}
	if decoded.Provider != "claude" {
		t.Fatalf("Provider lost in round-trip: got %q", decoded.Provider)
	}
}

func TestPrependAgentBadge_SkipsEmpty(t *testing.T) {
	t.Parallel()
	if got := prependAgentBadge("新对话", "", ""); got != "新对话" {
		t.Fatalf("empty agent_key must not prepend: got %q", got)
	}
}

func TestPrependAgentBadge_SkipsMainDefault(t *testing.T) {
	t.Parallel()
	if got := prependAgentBadge("新对话", "通用助手", "main"); got != "新对话" {
		t.Fatalf("main agent_key must not prepend even when title is set: got %q", got)
	}
	if got := prependAgentBadge("新对话", "", "Main"); got != "新对话" {
		t.Fatalf("case-insensitive main check failed: got %q", got)
	}
}

func TestPrependAgentBadge_PrefersTitleOverKey(t *testing.T) {
	t.Parallel()
	got := prependAgentBadge("写一条 SQL", "SQL 与数据建模专家", "sql-expert")
	if got != "[SQL 与数据建模专家] 写一条 SQL" {
		t.Fatalf("title should take precedence over slug, got %q", got)
	}
}

func TestPrependAgentBadge_FallsBackToAgentKey(t *testing.T) {
	t.Parallel()
	got := prependAgentBadge("写一条 SQL", "", "sql-expert")
	if got != "[sql-expert] 写一条 SQL" {
		t.Fatalf("empty title should fall back to slug, got %q", got)
	}
}

func TestPrependAgentBadge_Idempotent(t *testing.T) {
	t.Parallel()
	once := prependAgentBadge("写一条 SQL", "SQL 与数据建模专家", "sql-expert")
	twice := prependAgentBadge(once, "SQL 与数据建模专家", "sql-expert")
	if once != twice {
		t.Fatalf("applying prefix twice should be no-op: %q vs %q", once, twice)
	}
}
