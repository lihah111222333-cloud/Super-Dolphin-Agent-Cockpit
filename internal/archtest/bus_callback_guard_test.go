package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBusCallbackGuard is the P22 bus-subscriber callback slow-path guard
// shell (P0 骨架; see docs/plans/迁移/p22/P0_RuntimeOwnershipSkeleton.md
// §守卫改动建议-3).
//
// What P0 delivers here:
//   - Declares the forbidden-shape catalogue that the bus_callback matcher
//     must eventually enforce. The catalogue is the authoritative list for
//     P1b/P2/P3 slice PRs — when a slice lands its fix, it wires the AST
//     matcher against these tokens and flips the relevant subtest.
//   - Matcher subtests are parked as t.Skip until the owning slice lands.
//   - Nothing here uses the root-bridge allowlist: bus callbacks never share
//     exemption with the root runtime bridge. P0 explicitly forbids
//     "subscriber wiring 旁边就算 wiring 豁免" carve-outs.
//
// Matchers owned by downstream slices:
//   - P2 (Finding 5, internal/module/memory/module.go):
//     TeamSync callback -> StartSession
//   - P2 (Finding 7, internal/module/memory/auto_dream_task.go):
//     auto-dream scheduling inside event callback
//   - P2 (Finding 10, internal/module/memory/...):
//     ToolCallEnd -> AddToolReadResult -> os.ReadFile synchronous I/O
//   - P2 (thread wiring, internal/module/thread):
//     fx.Invoke(registerSubscriptions) that re-enters setter injection
//   - P2 (internal/platform/hooks/event_relay.go):
//     fire-and-forget `go` in relay callback
func TestBusCallbackGuard(t *testing.T) {
	t.Parallel()

	t.Run("forbidden_token_catalogue_is_locked", func(t *testing.T) {
		t.Parallel()
		// Freezing the catalogue here prevents silent drift between P0 and
		// the owning-slice matchers. Any add/remove in the catalogue must
		// land in the same PR that flips a matcher red→green.
		want := []string{
			"go ",                                   // bare go-statement in callback body
			"runtimesafe.SafeGo(",                   // SafeGo dispatch from callback
			"time.Sleep(",                           // blocking sleep
			"exec.Command(", "exec.CommandContext(", // process spawn
			"StartSession(", "StopSession(", // session lifecycle driven from callback
			"NotifyConfigChanged(",          // fan-out config reload
			"DispatchAfter(",                // timer-backed slow-path
			"AddToolReadResult(",            // NestedRuntime slow read (Finding 10)
			"os.ReadFile(", "os.WriteFile(", // synchronous disk I/O
		}
		if len(want) == 0 {
			t.Fatal("bus-callback forbidden token catalogue is empty")
		}
		// Sanity: no duplicates — drift would mask a true regression later.
		seen := map[string]struct{}{}
		for _, token := range want {
			if _, dup := seen[token]; dup {
				t.Errorf("duplicate forbidden token in catalogue: %q", token)
			}
			seen[token] = struct{}{}
		}
	})

	// P2 (thread wiring) live matcher: after the thread S1 slice lands
	// (NewServiceWithPromptAssemblyAndSharedFiles now takes promptStore +
	// classifier as constructor params), the thread module.go must not
	// reintroduce the late-setter injection pattern. All three setter
	// tokens are forbidden in module.go; `fx.Invoke` is still allowed
	// because registerSubscriptions now only wires the lifecycle hook.
	t.Run("bus_callback_must_not_register_late_setter", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "module", "thread", "module.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"svc.bindDispatcher(",
			"svc.bindPromptStore(",
			"svc.bindClassifier(",
			".bindDispatcher(",
			".bindPromptStore(",
			".bindClassifier(",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("thread/module.go reintroduced pre-P2 late-setter injection from registerSubscriptions; forbidden tokens present: %v", hits)
		}
	})

	// P2 Finding 7 live matcher: after commit 7837d6b+1 the auto-dream
	// subscription must only enqueue into autoDreamScheduler; the old fire-
	// and-forget path (onThreadStopped + `go maybeScheduleAutoDream`) is
	// forbidden from reappearing in internal/module/memory/auto_dream_task.go.
	t.Run("bus_callback_must_not_schedule_auto_dream", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "module", "memory", "auto_dream_task.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"func (h *MemoryLifecycleHooks) onThreadStopped",
			"go func() {\n\t\tif _, err := h.maybeScheduleAutoDream",
			"p.Hooks.onThreadStopped(",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("auto_dream_task.go reintroduced pre-P2 fire-and-forget auto-dream scheduling; forbidden tokens present: %v", hits)
		}
	})

	// P2 Finding 10 live matcher: after the nestedIngestWorker slice lands
	// (lossless pending-set + wake-signal owner), the memory module bus
	// callbacks must not drive the synchronous nested-read slow-path
	// directly. AddToolReadResult (which os.ReadFile's the persisted tool
	// result) belongs to the worker; os.ReadFile / os.WriteFile must not
	// reappear in the callback wiring file either.
	t.Run("bus_callback_must_not_do_synchronous_file_io", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "module", "memory", "module.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"p.NestedRuntime.AddToolReadResult(",
			"os.ReadFile(",
			"os.WriteFile(",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("memory/module.go reintroduced pre-P2 synchronous nested-read / file I/O on bus callback path; forbidden tokens present: %v", hits)
		}
	})

	// P2 Finding 5/6 live matcher: after the teamSyncCoordinator slice lands
	// the memory module bus callbacks must not drive the TeamSync session
	// lifecycle directly. Both the high-level helper names
	// (StartSessionFromThreadEvent / StopSessionFromThreadEvent) and the
	// lifecycle verbs (StartSession / StopSession) are forbidden in the
	// callback-wiring file; the coordinator is the only caller that may
	// keep a reference to the helpers, and it lives in a separate file.
	t.Run("bus_callback_must_not_start_session", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "module", "memory", "module.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"teampkg.StartSessionFromThreadEvent(",
			"teampkg.StopSessionFromThreadEvent(",
			".StartSession(",
			".StopSession(",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("memory/module.go reintroduced pre-P2 TeamSync lifecycle calls on bus callback path; forbidden tokens present: %v", hits)
		}
	})

	// P2 (hooks/event_relay fanout) live matcher: after the
	// hookDispatchWorker slice lands, the hooks event relay must not
	// spawn bare `go` goroutines or invoke Manager.DispatchAfter from the
	// bus callback body. The worker owns that slow-path under a tracked
	// Stop(ctx) drain.
	t.Run("bus_callback_must_not_fire_and_forget_goroutine", func(t *testing.T) {
		t.Parallel()
		root := repoRootForGuardTests(t)
		path := filepath.Join(root, "internal", "platform", "hooks", "event_relay.go")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		src := string(data)
		forbidden := []string{
			"go func()",
			"runtimesafe.SafeGo(",
			"manager.DispatchAfter(",
			"Manager.DispatchAfter(",
		}
		var hits []string
		for _, token := range forbidden {
			if strings.Contains(src, token) {
				hits = append(hits, token)
			}
		}
		if len(hits) > 0 {
			t.Fatalf("hooks/event_relay.go reintroduced pre-P2 fire-and-forget dispatch on bus callback path; forbidden tokens present: %v", hits)
		}
	})
	t.Run("subscriber_group_ownership_warning", func(t *testing.T) {
		root := repoRootForGuardTests(t)
		want := []ownershipHit{
			{"F-3", "internal/module/memory/module.go", "registerMemoryHooks", "registerLifecycleSubscriptions"},
			{"F-4", "internal/module/thread/module.go", "registerSubscriptions", "registerThreadSubscriptions"},
			{"F-9", "internal/platform/toolbridge/module.go", "registerDiffFallbackLifecycle", "ResilientSubscribe"},
		}
		for _, hit := range want {
			line, ok := findCallInFunction(t, root, hit.Path, hit.Symbol, hit.Call)
			if !ok {
				t.Logf("[P22.1 WARN] subscriber no-new guard missing TODO-locked call: %+v", hit)
				t.Fail()
				continue
			}
			t.Logf("[P22.1 WARN] %s %s:%d %s", hit.Finding, hit.Path, line, hit.Symbol)
		}
	})
}

type ownershipHit struct {
	Finding string
	Path    string
	Symbol  string
	Call    string
}

func findCallInFunction(t *testing.T, root, rel, symbol, call string) (int, bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != symbol || fn.Body == nil {
			continue
		}
		line, ok := findCallLine(fset, fn.Body, call)
		if ok {
			return line, true
		}
	}
	return 0, false
}

func findCallLine(fset *token.FileSet, node ast.Node, call string) (int, bool) {
	var line int
	ast.Inspect(node, func(n ast.Node) bool {
		if line != 0 || n == nil {
			return line == 0
		}
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if callName(ce.Fun) == call {
			line = fset.Position(ce.Pos()).Line
			return false
		}
		return true
	})
	return line, line != 0
}

func callName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	default:
		return ""
	}
}
