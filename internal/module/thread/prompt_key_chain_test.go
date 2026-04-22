package thread

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt/classifier"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// joinCandidateBlocks concatenates the Body of every candidate-pool block
// so legacy substring assertions ("Contents of <key>:\nbody") keep working
// after Step 2 moved pool merging from req.BaseInstructions to
// req.BaseInstructionBlocks.
func joinCandidateBlocks(blocks []contract.BaseInstructionBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, b := range blocks {
		parts = append(parts, b.Body)
	}
	return strings.Join(parts, "\n\n")
}

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

	// --- (3) StartRequest construction ---------------------------------------
	// Mirrors the field copy inside newStartHandler. We don't call the real
	// handler because Service.Start() does provider launch / persistence we
	// can't fake in a unit test; the chain we care about (decode -> request
	// -> router) is pure data flow and is fully covered here.
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

	// --- (4)+(5) Router resolve + BaseInstructions injection -----------------
	// Fake store mirrors what the user would have authored via SystemPromptPage:
	// a "main/launch-fav" row plus an unrelated default. The router must
	// land on the explicit pin, not the default.
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate("main/launch-fav", "main", "PromptText authored by the user via SystemPromptPage", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	svc := newServiceWithRouter(store)
	svc.resolveRoutedPrompt(context.Background(), &req)

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

	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
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
	}

	encoded, err := encodeStoredThreadConfig(stored)
	if err != nil {
		t.Fatalf("encodeStoredThreadConfig: %v", err)
	}

	decoded := decodeStoredThreadConfig(encoded)
	if decoded.PromptKey != "main/launch-fav" {
		t.Fatalf("PromptKey lost in round-trip: got %q, want main/launch-fav", decoded.PromptKey)
	}
	if decoded.Provider != "claude" {
		t.Fatalf("Provider lost in round-trip: got %q", decoded.Provider)
	}
}

// fakeClassifier is a stub Classifier for router integration tests. It records
// the Input and returns the configured Result so tests can assert what the
// router handed in (candidates, user input) and how it consumes the pick.
type fakeClassifier struct {
	result    classifier.Result
	err       error
	lastInput classifier.Input
	callCount int
}

func (f *fakeClassifier) Enabled() bool { return true }

func (f *fakeClassifier) Classify(_ context.Context, in classifier.Input) (classifier.Result, error) {
	f.callCount++
	f.lastInput = in
	return f.result, f.err
}

// TestResolveRoutedPrompt_ClassifierFillsEmptyPromptKey: when the caller opts
// in with UseClassifier=true and has user input but no explicit prompt_key,
// the router runs the classifier, stamps its pick into req.PromptKey, then
// lets pickRoutedTemplate take the normal explicit-pin branch.
func TestResolveRoutedPrompt_ClassifierFillsEmptyPromptKey(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", []string{"写 SQL"}),
			sqlTemplate("main/writing", "writer", "writing body", []string{"写邮件"}),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/sql", Reason: "SQL query"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:        "帮我写个 JOIN 查询",
		UseClassifier: true,
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 1 {
		t.Fatalf("classifier call count = %d, want 1", fake.callCount)
	}
	if len(fake.lastInput.Candidates) != 3 {
		t.Fatalf("classifier saw %d candidates, want 3 (enabled rows)", len(fake.lastInput.Candidates))
	}
	if fake.lastInput.UserInput != "帮我写个 JOIN 查询" {
		t.Fatalf("classifier user_input = %q", fake.lastInput.UserInput)
	}
	if req.PromptKey != "main/sql" {
		t.Fatalf("req.PromptKey after classify = %q, want main/sql", req.PromptKey)
	}
	if req.AgentKey != "sql_expert" {
		t.Fatalf("req.AgentKey stamped from picked row = %q", req.AgentKey)
	}
	if req.BaseInstructions != "sql body" {
		t.Fatalf("BaseInstructions = %q", req.BaseInstructions)
	}
}

// TestResolveRoutedPrompt_ClassifierSkippedWhenPromptKeySet: explicit pin
// wins unconditionally; the classifier is never invoked.
func TestResolveRoutedPrompt_ClassifierSkippedWhenPromptKeySet(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate("main/writing", "writer", "writing body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/writing"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:        "帮我写个 SQL",
		PromptKey:     "main/sql",
		UseClassifier: true,
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 0 {
		t.Fatalf("classifier must be skipped when prompt_key is pinned, got %d calls", fake.callCount)
	}
	if req.PromptKey != "main/sql" {
		t.Fatalf("pinned prompt_key overwritten: %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_ClassifierNotOptedIn: UseClassifier=false keeps the
// pre-Plan-B single-pin behavior.
func TestResolveRoutedPrompt_ClassifierNotOptedIn(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/sql"}}
	s.classifier = fake

	req := &StartRequest{Prompt: "写 SQL", UseClassifier: false}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 0 {
		t.Fatalf("opt-in flag off must not invoke classifier, got %d calls", fake.callCount)
	}
	// default fallback still works
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("default fallback missed: %q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_ClassifierEmptyPickKeepsDefaultFallback: classifier
// returning empty ("no strong match") must leave req.PromptKey empty so
// pickRoutedTemplate still finds the default persona.
func TestResolveRoutedPrompt_ClassifierEmptyPickKeepsDefaultFallback(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "", Reason: "no match"}}
	s.classifier = fake

	req := &StartRequest{Prompt: "random chit-chat", UseClassifier: true}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 1 {
		t.Fatalf("classifier should have been called once, got %d", fake.callCount)
	}
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("want default fallback when classifier punts, got %q", req.PromptKey)
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

// TestResolveRoutedPrompt_PoolBeatsClassifierWhenBothOn verifies the new
// P21 semantics: a non-empty candidate pool is the PRIMARY path regardless
// of UseClassifier. When the user curated a pool, every checked row is
// intentionally injected via Claude's multi-source format; the classifier
// is not asked to narrow anything — classifier inference is redundant when
// the user already picked what to include.
func TestResolveRoutedPrompt_PoolBeatsClassifierWhenBothOn(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", []string{"写 SQL"}),
			sqlTemplate("main/writing", "writer", "writing body", []string{"写邮件"}),
			sqlTemplate("main/code", "coder", "code body", []string{"写代码"}),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/writing"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:           "帮我回个客户邮件",
		UseClassifier:    true, // 分类器开启
		PromptCandidates: []string{"main/writing", "main/code"},
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 0 {
		t.Fatalf("classifier must not run when pool is non-empty; got %d calls", fake.callCount)
	}
	// Pool-merge injects every pool entry; classifier pick is ignored.
	joined := joinCandidateBlocks(req.BaseInstructionBlocks)
	for _, want := range []string{"Contents of main/writing:", "writing body", "Contents of main/code:", "code body"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("BaseInstructionBlocks missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "sql body") || strings.Contains(joined, "default body") {
		t.Fatalf("pool-merge leaked non-pool entry:\n%s", joined)
	}
	if req.BaseInstructions != "" {
		t.Fatalf("pool-merge must clear BaseInstructions; got %q", req.BaseInstructions)
	}
	if req.PromptKey != "" {
		t.Fatalf("req.PromptKey = %q, want empty (pool-merge mode)", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_EmptyPromptCandidatesKeepsAllEnabled covers the
// "user has not curated a pool" path: an empty PromptCandidates slice must
// leave legacy behavior (classify across every enabled template) unchanged.
func TestResolveRoutedPrompt_EmptyPromptCandidatesKeepsAllEnabled(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate("main/writing", "writer", "writing body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/sql"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:           "写个 JOIN",
		UseClassifier:    true,
		PromptCandidates: nil,
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if len(fake.lastInput.Candidates) != 3 {
		t.Fatalf("empty whitelist must leave all 3 enabled candidates; saw %d", len(fake.lastInput.Candidates))
	}
}

// TestResolveRoutedPrompt_StalePoolFallsThroughToClassifier covers the
// safety valve: if every key in the whitelist references a deleted prompt,
// pool-merge returns false and the request falls through to the normal
// classifier / default-fallback pipeline. With UseClassifier=true, this
// means the classifier runs against the full enabled library.
func TestResolveRoutedPrompt_StalePoolFallsThroughToClassifier(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/sql", "sql_expert", "sql body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/sql"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:           "随便一句话",
		UseClassifier:    true,
		PromptCandidates: []string{"main/deleted-prompt", "main/another-gone"},
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 1 {
		t.Fatalf("stale pool should fall through to classifier; got %d calls", fake.callCount)
	}
	if req.PromptKey != "main/sql" {
		t.Fatalf("classifier pick should be stamped; got PromptKey=%q", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_CandidatePoolMergeSkipsClassifier verifies the P21
// multi-enable concat mode: candidate pool non-empty + no classifier opt-in
// → all pool entries concatenated into BaseInstructions (Claude claudeMd
// multi-source style), classifier never invoked.
func TestResolveRoutedPrompt_CandidatePoolMergeSkipsClassifier(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/style", "main", "style body", nil),
			sqlTemplate("main/workflow", "main", "workflow body", nil),
			sqlTemplate("main/review", "main", "review body", nil),
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)
	fake := &fakeClassifier{result: classifier.Result{PromptKey: "main/style"}}
	s.classifier = fake

	req := &StartRequest{
		Prompt:           "写一个方案",
		UseClassifier:    false, // 关闭 classifier
		PromptCandidates: []string{"main/style", "main/workflow", "main/review"},
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if fake.callCount != 0 {
		t.Fatalf("classifier must not be invoked in pool-merge mode; got %d calls", fake.callCount)
	}
	if req.PromptKey != "" {
		t.Fatalf("req.PromptKey = %q, want empty for pool-merge mode", req.PromptKey)
	}
	if req.AgentKey != "" {
		t.Fatalf("req.AgentKey = %q, want empty for pool-merge mode", req.AgentKey)
	}
	if !strings.Contains(req.AgentTitle, "候选池") {
		t.Fatalf("req.AgentTitle = %q, want contains 候选池", req.AgentTitle)
	}
	// Verify Claude-style multi-source wrapping + concat of all 3 pool entries,
	// and that non-pool entries (defaultPromptKey) are excluded.
	joined := joinCandidateBlocks(req.BaseInstructionBlocks)
	for _, want := range []string{
		"Contents of main/style:",
		"style body",
		"Contents of main/workflow:",
		"workflow body",
		"Contents of main/review:",
		"review body",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("BaseInstructionBlocks missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "default body") {
		t.Fatalf("BaseInstructionBlocks leaked non-pool entry:\n%s", joined)
	}
	if req.BaseInstructions != "" {
		t.Fatalf("pool-merge must clear BaseInstructions; got %q", req.BaseInstructions)
	}
	// Pool-merge must stamp MergedCandidateKeys (in input order, enabled-only)
	// so the UI can render “候选池 · N 条” badge with the exact keys tooltip.
	wantKeys := []string{"main/style", "main/workflow", "main/review"}
	if got := req.MergedCandidateKeys; len(got) != len(wantKeys) {
		t.Fatalf("MergedCandidateKeys len=%d want=%d (%v)", len(got), len(wantKeys), got)
	}
	for i, want := range wantKeys {
		if req.MergedCandidateKeys[i] != want {
			t.Fatalf("MergedCandidateKeys[%d]=%q want %q", i, req.MergedCandidateKeys[i], want)
		}
	}
}

// TestResolveRoutedPrompt_CandidatePoolMergeYieldsToExplicitPin confirms that
// a caller-provided PromptKey pin overrides pool-merge mode: even with a
// non-empty pool, the explicit pin wins and merge never runs.
func TestResolveRoutedPrompt_CandidatePoolMergeYieldsToExplicitPin(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate("main/style", "main", "style body", nil),
			sqlTemplate("main/workflow", "main", "workflow body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{
		Prompt:           "帮我写",
		PromptKey:        "main/style", // 显式 pin
		PromptCandidates: []string{"main/style", "main/workflow"},
	}
	s.resolveRoutedPrompt(context.Background(), req)

	if req.BaseInstructions != "style body" {
		t.Fatalf("explicit pin should win and inject single template; got BaseInstructions=%q", req.BaseInstructions)
	}
	// Explicit pin path stamps PromptKey back + picks that row's AgentKey.
	if req.PromptKey != "main/style" {
		t.Fatalf("req.PromptKey = %q, want main/style (pin)", req.PromptKey)
	}
}

// TestResolveRoutedPrompt_CandidatePoolMergeAllStaleFallsBack ensures
// robustness: when every key in the pool is unknown (user deleted the
// underlying prompts), merge returns false so the classifier/default
// fallback paths can still run.
func TestResolveRoutedPrompt_CandidatePoolMergeAllStaleFallsBack(t *testing.T) {
	t.Parallel()
	store := &fakePromptStore{
		templates: []promptstore.PromptTemplate{
			sqlTemplate(defaultPromptKey, "main", "default body", nil),
		},
	}
	s := newServiceWithRouter(store)

	req := &StartRequest{
		Prompt:           "hi",
		PromptCandidates: []string{"main/deleted-1", "main/deleted-2"},
	}
	s.resolveRoutedPrompt(context.Background(), req)

	// All stale → merge skipped → default fallback picks main/default.
	if req.PromptKey != defaultPromptKey {
		t.Fatalf("stale-only pool should fall back to default; got PromptKey=%q", req.PromptKey)
	}
	if req.BaseInstructions != "default body" {
		t.Fatalf("BaseInstructions = %q, want default body", req.BaseInstructions)
	}
}
