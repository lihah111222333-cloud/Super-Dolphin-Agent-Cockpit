# mcp-orch P0/P1 Remediation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the confirmed P1 risks found in `cmd/mcp-orch` review so the peer HTTP, cron, memory, and notify paths have no known P0/P1 issues.

**Architecture:** Fix security boundaries at the transport and filesystem edges, then fix scheduler correctness with deterministic idempotency and fail-fast locking. Keep changes narrow: shared HTTP transport/auth stays in `internal/mcpserver/common` and `internal/platform/discovery`; cron correctness stays in `internal/sidecar/orch/orchestration/cron` plus the orchestration adapter; memory and notify fixes stay in their existing packages.

**Tech Stack:** Go 1.25.7, fx, pgx/pgxpool, robfig/cron, existing `taskdag` stores, `./scripts/test_with_guard.sh`.

---

## Review Result Summary

No confirmed P0 was found.

Confirmed P1 issues to fix:

1. Peer HTTP MCP endpoint is unauthenticated in peer mode.
   Evidence: `cmd/mcp-orch/http_runner.go:41` starts `common.NewHTTPServer(...)`; `internal/mcpserver/common/http_transport.go:83-129` accepts POST `/mcp` and dispatches `tools/list` and `tools/call` without token checks; `internal/platform/discovery/discovery.go:30-36` writes the address to `/tmp` with `0644`.

2. Scheduled DAG can be skipped if `next_run_at` is advanced before `StartDAG` fails.
   Evidence: `internal/sidecar/orch/orchestration/cron/scheduler_cron.go:404-412` calls `UpdateNextRun` before `StartDAG`.

3. Cron advisory lock release can fail under canceled/deadline context and leave the session lock held.
   Evidence: `scheduler_cron.go:373` defers release with the tick context; `scheduler_cron.go:398-400` calls `Unlock(ctx)`; `scheduler_cron.go:511-514` runs `pg_advisory_unlock` on that context.

4. Scheduled ticker permits nil locker, allowing duplicate multi-instance scheduled execution if production wiring is missed.
   Evidence: `scheduler_cron.go:338-351` does not reject `cfg.Locker == nil`; `scheduler_cron.go:387-390` treats nil locker as acquired.

5. Memory name lookup can read `.md` symlinks outside the memory root.
   Evidence: `cmd/mcp-orch/memory/entry_file.go:28-35` walks `.md` files and calls `readEntryFile(path)`; `entry_file.go:49-54` uses `os.ReadFile`/`os.Stat`, which follows symlinks; this bypasses `validateMemoryWritePath` used only by path lookup in `service.go:110-120`.

6. DAG notification intake queue is unbounded before the bounded platform notifier queue.
   Evidence: `internal/sidecar/orch/notify/subscribers.go:167-169` appends to `DAGNotifier.queue` without a limit; the single worker then performs DB lookups and `TryEnqueue`.

Not treated as confirmed P1:

- `ArchiveAgent` optional `agentThreads` / `agentBindings` nil paths. Production `cmd/mcp-orch/fx.go:77-83` provides both stores, and `orchestration/service.go:124-125` optional tags only preserve bare test construction. Keep this as a P2 cleanup unless a production wiring path without those stores is discovered.

---

### Task 1: Peer HTTP Auth And Secure Discovery

**Files:**
- Modify: `internal/mcpserver/common/http_transport.go`
- Modify: `internal/platform/discovery/discovery.go`
- Modify: `cmd/mcp-orch/http_runner.go`
- Modify: `internal/dto/provider/manifest.go`
- Modify: `internal/contract/manifest.go`
- Modify: `internal/module/turn/manifest.go`
- Test: `internal/mcpserver/common/server_test.go`
- Test: `internal/platform/discovery/discovery_test.go`
- Test: `internal/provider/unified/manifest_test.go`

- [ ] **Step 1: Write failing HTTP auth tests**

Add tests in `internal/mcpserver/common/server_test.go`:

```go
func TestHTTPServerRejectsToolsWithoutBearerToken(t *testing.T) {
	server := NewHTTPServer("mcp-orch", "dev", testToolProvider{}, WithBearerToken("secret"))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	rec := httptest.NewRecorder()

	server.handleMCP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHTTPServerAcceptsToolsWithBearerToken(t *testing.T) {
	server := NewHTTPServer("mcp-orch", "dev", testToolProvider{}, WithBearerToken("secret"))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()

	server.handleMCP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}
```

Run: `./scripts/test_with_guard.sh ./internal/mcpserver/common -run 'TestHTTPServerRejectsToolsWithoutBearerToken|TestHTTPServerAcceptsToolsWithBearerToken' -count=1`

Expected: FAIL because `WithBearerToken` does not exist and unauthenticated requests are accepted.

- [ ] **Step 2: Implement fail-closed bearer auth**

In `internal/mcpserver/common/http_transport.go`, add option plumbing and check before JSON-RPC dispatch:

```go
type HTTPServerOption func(*HTTPServer)

func WithBearerToken(token string) HTTPServerOption {
	return func(h *HTTPServer) {
		h.bearerToken = strings.TrimSpace(token)
	}
}

type HTTPServer struct {
	name        string
	version     string
	tools       ToolProvider
	server      *http.Server
	bearerToken string
}

func NewHTTPServer(name, version string, tools ToolProvider, opts ...HTTPServerOption) *HTTPServer {
	// keep existing name/version normalization
	h := &HTTPServer{name: name, version: version, tools: tools}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func (h *HTTPServer) authorized(r *http.Request) bool {
	if strings.TrimSpace(h.bearerToken) == "" {
		return true
	}
	got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.bearerToken)) == 1
}
```

Call `authorized` in `handleMCP` after the method check and before reading the body. Return `401` and no JSON-RPC body for missing or wrong auth.

- [ ] **Step 3: Make peer mode require a session token**

In `cmd/mcp-orch/http_runner.go`, load `GO_AGENT_CTL_SESSION_TOKEN` and fail fast in peer mode:

```go
func newHTTPRunner(registry tools.Registry) platformrunner.Runner {
	if os.Getenv("GO_AGENT_PEER_MODE") != "1" {
		return blockRunner{}
	}
	return &httpRunner{
		tools:       registryToolProvider{registry: registry},
		bearerToken: strings.TrimSpace(os.Getenv("GO_AGENT_CTL_SESSION_TOKEN")),
	}
}

func (r *httpRunner) Run(ctx context.Context) error {
	if strings.TrimSpace(r.bearerToken) == "" {
		return errors.New("mcp-orch http: GO_AGENT_CTL_SESSION_TOKEN required in peer mode")
	}
	srv := common.NewHTTPServer(httpBinaryName, "dev", r.tools, common.WithBearerToken(r.bearerToken))
	// existing start/stop flow
}
```

Add the `bearerToken string` field to `httpRunner`. Import `errors` and `strings`.

- [ ] **Step 4: Lock discovery file permissions and probe with auth**

In `internal/platform/discovery/discovery.go`, change discovery file writes to `0600`:

```go
if err := os.WriteFile(tmp, []byte(strings.TrimSpace(addr)+"\n"), 0o600); err != nil {
	return err
}
```

Add `ProbePeerHTTPAddrWithToken(addr, token string) error` and make `ProbePeerHTTPAddr` call it with an empty token. When token is non-empty, set `Authorization: Bearer <token>`.

Add `DiscoverPeerHTTPAddrWithToken(binaryName, token string) (string, error)` and `DiscoverPeerHTTPAddrForParentWithToken(binaryName string, parentPID int, token string) (string, error)`; keep existing functions as compatibility wrappers.

- [ ] **Step 5: Carry auth headers in HTTP manifests**

In `internal/dto/provider/manifest.go`, add:

```go
Headers map[string]string `json:"headers,omitempty"`
PeerHTTPTokens map[ToolFamily]string
```

`Headers` belongs to `MCPBinary`; `PeerHTTPTokens` belongs to `ManifestContext`.

In `internal/contract/manifest.go`, when emitting a direct peer HTTP binary, include the header only when the token exists:

```go
headers := map[string]string(nil)
if token := strings.TrimSpace(ctx.PeerHTTPTokens[fam]); token != "" {
	headers = map[string]string{"Authorization": "Bearer " + token}
}
bins = append(bins, dto.MCPBinary{
	Name: serverName, Type: "http", URL: "http://" + addr + "/mcp",
	Headers: headers,
	AutoApprove: append([]string(nil), autoApprove...),
})
```

In `internal/module/turn/manifest.go`, make peer discovery use `GO_AGENT_CTL_SESSION_TOKEN`:

```go
func discoverPeers() (map[dto.ToolFamily]string, map[dto.ToolFamily]string) {
	token := strings.TrimSpace(os.Getenv("GO_AGENT_CTL_SESSION_TOKEN"))
	addrs := make(map[dto.ToolFamily]string)
	tokens := make(map[dto.ToolFamily]string)
	for _, fam := range []dto.ToolFamily{dto.FamilyLSP, dto.FamilyOrch} {
		addr, err := discovery.DiscoverPeerHTTPAddrWithToken("mcp-"+string(fam), token)
		if err != nil || addr == "" {
			continue
		}
		addrs[fam] = addr
		if token != "" {
			tokens[fam] = token
		}
	}
	return addrs, tokens
}
```

If the current production path intentionally stays `ManifestTransportStdioOnly`, keep that mode unchanged and still add tests around explicit `PeerHTTPAddrs`.

- [ ] **Step 6: Run focused auth tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/mcpserver/common ./internal/platform/discovery ./internal/contract ./internal/provider/manifestbuilder ./internal/module/turn -run 'TestHTTPServerRejectsToolsWithoutBearerToken|TestHTTPServerAcceptsToolsWithBearerToken|TestBuildManifest_UsesPeerHTTPAuthHeader|TestWriteDiscoveryFileUsesOwnerOnlyPermissions' -count=1
```

Expected: PASS.

---

### Task 2: Scheduled DAG Trigger Consistency

**Files:**
- Modify: `internal/sidecar/orch/orchestration/cron/scheduler_cron.go`
- Modify: `internal/sidecar/orch/orchestration/scheduler.go`
- Modify: `internal/sidecar/orch/store/taskdag/contract.go`
- Modify: `internal/sidecar/orch/store/taskdag/store_run.go` or the existing run transaction store file
- Test: `internal/sidecar/orch/orchestration/cron/scheduler_cron_test.go`
- Test: `internal/sidecar/orch/orchestration/dag_start_test.go`

- [ ] **Step 1: Write failing regression for StartDAG failure**

In `internal/sidecar/orch/orchestration/cron/scheduler_cron_test.go`, add:

```go
func TestScheduledDAGTicker_DoesNotAdvanceNextRunWhenStartFails(t *testing.T) {
	startErr := errors.New("start failed")
	store := &fakeScheduleStore{due: []DueDAG{{DagKey: "daily-report", CronExpr: "0 8 * * *", DueAt: time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)}}}
	starter := &fakeStarter{err: startErr}
	ticker, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{Store: store, Starter: starter, Locker: &fakeLocker{}})
	if err != nil {
		t.Fatalf("NewScheduledDAGTicker: %v", err)
	}

	_, err = ticker.Tick(context.Background(), time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC))
	if !errors.Is(err, startErr) {
		t.Fatalf("Tick err = %v, want startErr", err)
	}
	if len(store.updates) != 0 {
		t.Fatalf("next_run_at updates = %+v, want none when StartDAG fails", store.updates)
	}
}
```

Run: `./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron -run TestScheduledDAGTicker_DoesNotAdvanceNextRunWhenStartFails -count=1`

Expected: FAIL until `triggerDueDAG` starts before it advances schedule.

- [ ] **Step 2: Include due time in scanned rows**

Change `DueDAG` in `scheduler_cron.go`:

```go
type DueDAG struct {
	DagKey   string
	CronExpr string
	DueAt    time.Time
}
```

Change `SQLDAGScheduleStore.DueDAGs` to select and scan `next_run_at`:

```sql
SELECT dag_key, cron_expr, next_run_at
FROM task_dags
WHERE trigger = 'scheduled'
  AND cron_expr <> ''
  AND next_run_at IS NOT NULL
  AND next_run_at <= $1
ORDER BY next_run_at ASC, id ASC
```

Update fake stores and SQL tests to expect `DueAt`.

- [ ] **Step 3: Add deterministic scheduled idempotency key**

Add helper in `scheduler_cron.go`:

```go
func scheduledIdempotencyKey(dag DueDAG) string {
	dueAt := dag.DueAt.UTC().Format(time.RFC3339Nano)
	return "scheduled:" + strings.TrimSpace(dag.DagKey) + ":" + dueAt
}
```

Import `strings` if needed.

- [ ] **Step 4: Extend cron starter request**

Replace the cron package starter interface with a request value:

```go
type ScheduledDAGStartRequest struct {
	DagKey         string
	TriggerSource  string
	IdempotencyKey string
	DueAt          time.Time
	NextRunAt      time.Time
}

type DAGStarter interface {
	StartDAG(ctx context.Context, req ScheduledDAGStartRequest) error
}
```

Update test fakes to record `ScheduledDAGStartRequest`.

- [ ] **Step 5: Start first, advance second**

Change `triggerDueDAG`:

```go
func (t *ScheduledDAGTicker) triggerDueDAG(ctx context.Context, dag DueDAG, now time.Time) error {
	nextRunAt, err := t.nextRunAt(dag, now)
	if err != nil {
		return err
	}
	req := ScheduledDAGStartRequest{
		DagKey:         strings.TrimSpace(dag.DagKey),
		TriggerSource:  scheduledTriggerSource,
		IdempotencyKey: scheduledIdempotencyKey(dag),
		DueAt:          dag.DueAt,
		NextRunAt:      nextRunAt,
	}
	if err := t.starter.StartDAG(ctx, req); err != nil {
		return err
	}
	if err := t.store.UpdateNextRun(ctx, dag.DagKey, nextRunAt); err != nil {
		return classifyTickError(TickErrorClassInfrastructure, "update_next_run_at", err)
	}
	return nil
}
```

This removes the immediate skip. A stronger follow-up can move both operations into one DB transaction; until then, deterministic idempotency prevents duplicate starts when `UpdateNextRun` fails after a successful start.

- [ ] **Step 6: Add orchestration adapter preserving idempotency**

Where production cron wiring is added or already exists, implement the adapter so it calls:

```go
_, err := svc.StartDAG(ctx, contract.StartDAGRequest{
	DagKey:         req.DagKey,
	TriggerSource: req.TriggerSource,
	IdempotencyKey: req.IdempotencyKey,
})
return err
```

If production cron wiring is still absent, add the adapter in the orchestration package next to other scheduler wiring and test it with a fake service before enabling cron.

- [ ] **Step 7: Run scheduled trigger tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron ./internal/sidecar/orch/orchestration -run 'TestScheduledDAGTicker_|TestStartDAG_' -count=1
```

Expected: PASS, including the new failure-before-advance regression.

---

### Task 3: Advisory Lock Release Must Survive Cancellation

**Files:**
- Modify: `internal/sidecar/orch/orchestration/cron/scheduler_cron.go`
- Test: `internal/sidecar/orch/orchestration/cron/scheduler_cron_test.go`

- [ ] **Step 1: Write failing canceled-unlock test**

Add a lock handle fake that records the context state:

```go
type contextCheckingLockHandle struct {
	errOnCanceled bool
	unlockCalls   int
}

func (h *contextCheckingLockHandle) Unlock(ctx context.Context) error {
	h.unlockCalls++
	if h.errOnCanceled && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func TestScheduledDAGTicker_UnlockUsesFreshCleanupContext(t *testing.T) {
	handle := &contextCheckingLockHandle{errOnCanceled: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var result error

	(&ScheduledDAGTicker{}).releaseAdvisoryLock(ctx, handle, &result)

	if result != nil {
		t.Fatalf("release result = %v, want nil from fresh cleanup context", result)
	}
	if handle.unlockCalls != 1 {
		t.Fatalf("unlockCalls = %d, want 1", handle.unlockCalls)
	}
}
```

Run: `./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron -run TestScheduledDAGTicker_UnlockUsesFreshCleanupContext -count=1`

Expected: FAIL because the current release uses the canceled context.

- [ ] **Step 2: Use bounded cleanup context**

Add a constant:

```go
const advisoryUnlockTimeout = 5 * time.Second
```

Change release:

```go
func (t *ScheduledDAGTicker) releaseAdvisoryLock(_ context.Context, handle AdvisoryLockHandle, result *error) {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), advisoryUnlockTimeout)
	defer cancel()
	if unlockErr := handle.Unlock(cleanupCtx); unlockErr != nil && *result == nil {
		*result = classifyTickError(TickErrorClassInfrastructure, "advisory_unlock", unlockErr)
	}
}
```

- [ ] **Step 3: Discard pg connection when unlock fails**

Update `pgAdvisoryLockHandle.Unlock` so a failed unlock does not return a possibly locked session to the pool:

```go
func (h *pgAdvisoryLockHandle) Unlock(ctx context.Context) error {
	var release = true
	defer func() {
		if release {
			h.conn.Release()
		}
	}()
	var unlocked bool
	if err := h.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, h.lockID).Scan(&unlocked); err != nil {
		raw := h.conn.Hijack()
		release = false
		_ = raw.Close(context.Background())
		return err
	}
	if !unlocked {
		return errors.New("cron: advisory lock was not held")
	}
	return nil
}
```

If `pgxpool.Conn.Hijack` is unavailable in the pinned pgx version, use the supported pgxpool method for destroying/removing a connection; do not call `Release` after a failed unlock.

- [ ] **Step 4: Run lock tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron -run 'TestScheduledDAGTicker_ReleaseOnExit|TestScheduledDAGTicker_UnlockUsesFreshCleanupContext|TestScheduledDAGTicker_MultiInstance_OneAcquires' -count=1
```

Expected: PASS.

---

### Task 4: Scheduled Ticker Must Require A Locker

**Files:**
- Modify: `internal/sidecar/orch/orchestration/cron/scheduler_cron.go`
- Test: `internal/sidecar/orch/orchestration/cron/scheduler_cron_test.go`

- [ ] **Step 1: Write failing nil-locker test**

Add:

```go
func TestNewScheduledDAGTickerRejectsNilLocker(t *testing.T) {
	_, err := NewScheduledDAGTicker(ScheduledDAGTickerConfig{
		Store:   &fakeScheduleStore{},
		Starter: &fakeStarter{},
		Locker:  nil,
	})
	if !errors.Is(err, ErrNilLockPool) {
		t.Fatalf("err = %v, want ErrNilLockPool", err)
	}
}
```

Run: `./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron -run TestNewScheduledDAGTickerRejectsNilLocker -count=1`

Expected: FAIL because nil currently means no-op acquired.

- [ ] **Step 2: Fail fast on missing locker**

Change constructor:

```go
if cfg.Locker == nil {
	return nil, ErrNilLockPool
}
```

Remove the nil branch from `tryAdvisoryLock`.

- [ ] **Step 3: Make tests explicit**

Update every `NewScheduledDAGTicker` call in `scheduler_cron_test.go` to pass `Locker: &fakeLocker{}` unless the test is specifically checking multi-instance behavior.

- [ ] **Step 4: Run cron constructor tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/orchestration/cron -run 'TestNewScheduledDAGTickerRejectsNilLocker|TestScheduledDAGTicker_' -count=1
```

Expected: PASS.

---

### Task 5: Memory Scan Must Reject Symlink Escape

**Files:**
- Modify: `cmd/mcp-orch/memory/entry_file.go`
- Modify: `cmd/mcp-orch/memory/service.go` if function signatures need root propagation
- Test: `cmd/mcp-orch/memory/service_test.go`

- [ ] **Step 1: Write failing symlink escape regression**

Add in `service_test.go`:

```go
func TestServiceReadByNameRejectsSymlinkEscape(t *testing.T) {
	svc := newTestMemoryService(t)
	root := userScopeFixtureRoot(t)
	outside := filepath.Join(t.TempDir(), "secret.md")
	writeMemoryFixture(t, outside, `---
name: Secret
type: user
---
outside secret`)
	if err := os.MkdirAll(filepath.Join(root, "user"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "user", "secret.md")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := svc.Read(context.Background(), contract.MemoryReadRequest{
		Name:  "Secret",
		Scope: contract.MemoryScopeUser,
	})
	if err == nil {
		t.Fatalf("Read() error = nil, want symlink escape rejection")
	}
}
```

Run: `./scripts/test_with_guard.sh ./cmd/mcp-orch/memory -run TestServiceReadByNameRejectsSymlinkEscape -count=1`

Expected: FAIL because `os.ReadFile` follows the symlink.

- [ ] **Step 2: Validate every scanned entry against root**

Change `scanEntries` to compute a real root once and reject symlink files:

```go
func scanEntries(root string) ([]diskEntry, error) {
	rootReal, err := resolveRealPath(root)
	if err != nil {
		return nil, err
	}
	// existing os.Stat(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if filepath.Ext(path) != ".md" || filepath.Base(path) == memoryIndexFileName {
			return nil
		}
		entry, err := readEntryFileWithinRoot(rootReal, path)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	// existing sort/return
}
```

Add:

```go
func readEntryFileWithinRoot(rootReal, path string) (diskEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return diskEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return diskEntry{}, fmt.Errorf("%w: symlink memory entry %s", contract.ErrMemoryInvalidParam, path)
	}
	pathReal, err := resolveRealPath(path)
	if err != nil {
		return diskEntry{}, err
	}
	if !platformshared.ContainsPath(rootReal, pathReal) {
		return diskEntry{}, fmt.Errorf("%w: memory entry escapes root", contract.ErrMemoryInvalidParam)
	}
	return readEntryFile(path)
}
```

Import `fmt` and `platformshared` in `entry_file.go`.

- [ ] **Step 3: Run memory tests**

Run:

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch/memory -count=1
```

Expected: PASS.

---

### Task 6: Bound DAG Notifier Intake Queue

**Files:**
- Modify: `internal/sidecar/orch/notify/subscribers.go`
- Test: `internal/sidecar/orch/notify/subscribers_test.go`

- [ ] **Step 1: Write failing queue bound regression**

Add a constructor option and test target first:

```go
func TestDAGNotifierDropsWhenQueueFull(t *testing.T) {
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			time.Sleep(50 * time.Millisecond)
			return []taskdag.Node{{NodeKey: "n", Config: jsonB(t, map[string]any{"notify_channel": "ch"})}}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store, WithDAGNotifyQueueCapacity(1))
	n.Start()
	defer func() { _ = n.Stop(context.Background()) }()

	for i := 0; i < 100; i++ {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "done",
		})
	}

	if n.Metrics().Dropped == 0 {
		t.Fatalf("Dropped = 0, want queue overflow drops")
	}
}
```

Run: `./scripts/test_with_guard.sh ./internal/sidecar/orch/notify -run TestDAGNotifierDropsWhenQueueFull -count=1`

Expected: FAIL because no queue capacity or dropped metric exists.

- [ ] **Step 2: Add bounded queue option and metric**

In `subscribers.go`:

```go
const defaultDAGNotifyQueueCapacity = 1024

type DAGNotifierOption func(*DAGNotifier)

func WithDAGNotifyQueueCapacity(capacity int) DAGNotifierOption {
	return func(n *DAGNotifier) {
		if capacity > 0 {
			n.queueCapacity = capacity
		}
	}
}
```

Add fields:

```go
queueCapacity int
dropped       atomic.Int64
```

Change constructor signature to:

```go
func NewDAGNotifier(logger *slog.Logger, notifier contract.MessageNotifier, store taskdag.Store, opts ...DAGNotifierOption) *DAGNotifier
```

Set default capacity and apply options.

- [ ] **Step 3: Drop and count when full**

Change `onNodeStatusChanged`:

```go
n.mu.Lock()
if len(n.queue) >= n.queueCapacity {
	n.mu.Unlock()
	n.dropped.Add(1)
	n.logger.Warn("notify(orch): dag notifier queue full; dropping event",
		slog.String("dag_key", strings.TrimSpace(ev.DagKey)),
		slog.String("node_key", strings.TrimSpace(ev.NodeKey)))
	return
}
n.queue = append(n.queue, dagNotifyRequest{ev: ev})
n.mu.Unlock()
```

Add `Dropped int64` to `Metrics` and `Metrics()`.

- [ ] **Step 4: Bound DB lookup time**

Add a small timeout around `processEvent` store lookups:

```go
const dagNotifyProcessTimeout = 5 * time.Second

ctx, cancel := context.WithTimeout(context.Background(), dagNotifyProcessTimeout)
defer cancel()
```

This prevents a stuck store from blocking the single worker forever.

- [ ] **Step 5: Run notify tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/notify -count=1
```

Expected: PASS.

---

### Task 7: Final Verification

**Files:**
- No new production files beyond tasks above.
- Do not stage unrelated existing changes in `docs/doc/codemap/*` or other pre-existing dirty files unless the user explicitly asks.

- [ ] **Step 1: Run affected package tests**

Run:

```bash
./scripts/test_with_guard.sh ./internal/mcpserver/common ./internal/platform/discovery ./internal/contract ./internal/provider/manifestbuilder ./internal/module/turn ./cmd/mcp-orch/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run guard**

Run:

```bash
make guard
```

Expected: PASS.

- [ ] **Step 3: Inspect baseline and status**

Run:

```bash
git status --short
git diff -- internal/archtest/baseline.json
```

Expected: no unexpected baseline growth; unrelated pre-existing user changes remain untouched.

---

## Completion Criteria

- Peer HTTP `tools/list` and `tools/call` fail closed without a valid bearer token in peer mode.
- Discovery files are not world-readable and health probes can authenticate when a token is required.
- Scheduled DAG runs use deterministic scheduled idempotency keys and do not advance `next_run_at` before a failed start.
- Advisory locks are released with a fresh bounded cleanup context, and failed unlocks do not return possibly locked sessions to the pool.
- Scheduled ticker construction rejects nil lockers outside explicit tests.
- Memory scan-by-name cannot read symlinked files outside the memory root.
- DAG notifier has a bounded intake queue and exposes a drop metric.
- All affected package tests and `make guard` pass.
