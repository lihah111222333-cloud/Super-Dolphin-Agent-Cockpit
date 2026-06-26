package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestBusCallbackGuard 防止 bus subscriber 回调重新引入慢路径副作用。
// 它锁定禁止 token 目录，并把已落地的 matcher 绑定到当前真实代码路径。
func TestBusCallbackGuard(t *testing.T) {
	t.Parallel()

	runBusCallbackGuardSubtest(t, "forbidden_token_catalogue_is_locked", assertForbiddenTokenCatalogueLocked)

	// thread 模块的订阅注册不能再通过回调后置注入 promptStore 或 classifier。
	// 构造函数注入是当前真实形态，fx.Invoke 只允许保留生命周期 hook wiring。
	runBusCallbackGuardSubtest(t, "bus_callback_must_not_register_late_setter", func(t *testing.T) {
		assertFileLacksForbiddenTokens(t, "internal/module/thread/module.go", []string{
			"svc.bindDispatcher(",
			"svc.bindPromptStore(",
			"svc.bindClassifier(",
			".bindDispatcher(",
			".bindPromptStore(",
			".bindClassifier(",
		}, "thread/module.go reintroduced pre-P2 late-setter injection from registerSubscriptions")
	})

	// auto-dream 的事件回调只能进入调度器队列，不能在回调内直接启动后台调度。
	runBusCallbackGuardSubtest(t, "bus_callback_must_not_schedule_auto_dream", func(t *testing.T) {
		assertFileLacksForbiddenTokens(t, "internal/module/memory/auto_dream_task.go", []string{
			"func (h *MemoryLifecycleHooks) onThreadStopped",
			"go func() {\n\t\tif _, err := h.maybeScheduleAutoDream",
			"p.Hooks.onThreadStopped(",
		}, "auto_dream_task.go reintroduced pre-P2 fire-and-forget auto-dream scheduling")
	})

	// memory bus 回调不能直接触发 nested tool result 的同步读写慢路径。
	// worker 拥有 AddToolReadResult 和磁盘 I/O，callback wiring 文件只负责轻量转发。
	runBusCallbackGuardSubtest(t, "bus_callback_must_not_do_synchronous_file_io", func(t *testing.T) {
		assertFileLacksForbiddenTokens(t, "internal/module/memory/module.go", []string{
			"p.NestedRuntime.AddToolReadResult(",
			"os.ReadFile(",
			"os.WriteFile(",
		}, "memory/module.go reintroduced pre-P2 synchronous nested-read / file I/O on bus callback path")
	})

	// TeamSync 会话生命周期由 coordinator 管理，memory bus 回调不能直接 start/stop。
	// callback-wiring 文件禁止保留高层 helper 和底层生命周期动词，避免绕过 coordinator。
	runBusCallbackGuardSubtest(t, "bus_callback_must_not_start_session", func(t *testing.T) {
		assertFileLacksForbiddenTokens(t, "internal/module/memory/module.go", []string{
			"teampkg.StartSessionFromThreadEvent(",
			"teampkg.StopSessionFromThreadEvent(",
			".StartSession(",
			".StopSession(",
		}, "memory/module.go reintroduced pre-P2 TeamSync lifecycle calls on bus callback path")
	})

	// hooks event relay 的慢路径由 dispatch worker 承担，bus 回调体不能再起裸 goroutine。
	// DispatchAfter 也必须留在可 drain 的 worker 内，避免停止阶段丢失后台任务。
	runBusCallbackGuardSubtest(t, "bus_callback_must_not_fire_and_forget_goroutine", func(t *testing.T) {
		assertFileLacksForbiddenTokens(t, "internal/platform/hooks/event_relay.go", []string{
			"go func()",
			"runtimesafe.SafeGo(",
			"manager.DispatchAfter(",
			"Manager.DispatchAfter(",
		}, "hooks/event_relay.go reintroduced pre-P2 fire-and-forget dispatch on bus callback path")
	})
	runBusCallbackGuardSubtest(t, "subscriber_group_ownership_warning", func(t *testing.T) {
		assertSubscriberGroupOwnership(t)
	})
}

func runBusCallbackGuardSubtest(t *testing.T, name string, fn func(*testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		fn(t)
	})
}

func assertForbiddenTokenCatalogueLocked(t *testing.T) {
	// 禁止 token 目录必须和 matcher 同步维护，避免回调慢路径规则静默漂移。
	want := []string{
		"go ",                                   // 回调体内裸 go 语句
		"runtimesafe.SafeGo(",                   // 从回调直接派发 SafeGo
		"time.Sleep(",                           // 阻塞等待
		"exec.Command(", "exec.CommandContext(", // 启动外部进程
		"StartSession(", "StopSession(", // 从回调直接驱动会话生命周期
		"NotifyConfigChanged(",          // 回调内广播配置重载
		"DispatchAfter(",                // timer 支撑的慢路径
		"AddToolReadResult(",            // NestedRuntime 慢读路径
		"os.ReadFile(", "os.WriteFile(", // 同步磁盘 I/O
	}
	if len(want) == 0 {
		t.Fatal("bus-callback forbidden token catalogue is empty")
	}
	seen := map[string]struct{}{}
	for _, token := range want {
		if _, dup := seen[token]; dup {
			t.Errorf("duplicate forbidden token in catalogue: %q", token)
		}
		seen[token] = struct{}{}
	}
}

func assertFileLacksForbiddenTokens(t *testing.T, rel string, forbidden []string, message string) {
	t.Helper()
	path := filepath.Join(repoRootForGuardTests(t), filepath.FromSlash(rel))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if hits := forbiddenTokenHits(string(data), forbidden); len(hits) > 0 {
		t.Fatalf("%s; forbidden tokens present: %v", message, hits)
	}
}

func forbiddenTokenHits(src string, forbidden []string) []string {
	var hits []string
	for _, token := range forbidden {
		if strings.Contains(src, token) {
			hits = append(hits, token)
		}
	}
	return hits
}

func assertSubscriberGroupOwnership(t *testing.T) {
	root := repoRootForGuardTests(t)
	hits := findLifecycleOnStartCallHits(t, root, map[string]bool{
		"Subscribe":          true,
		"ResilientSubscribe": true,
	})
	for _, hit := range hits {
		t.Errorf("subscriber ownership regression: %s:%d OnStart calls %s", hit.Path, hit.Line, hit.Call)
	}
}

func callName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	case *ast.IndexExpr:
		return callName(x.X)
	case *ast.IndexListExpr:
		return callName(x.X)
	default:
		return ""
	}
}

func callReceiverName(expr ast.Expr) string {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		ident, ok := x.X.(*ast.Ident)
		if !ok {
			return ""
		}
		return ident.Name
	case *ast.IndexExpr:
		return callReceiverName(x.X)
	case *ast.IndexListExpr:
		return callReceiverName(x.X)
	default:
		return ""
	}
}

type lifecycleCallHit struct {
	Path     string
	Line     int
	Call     string
	Receiver string
}

func findLifecycleOnStartCallHits(t *testing.T, root string, forbidden map[string]bool) []lifecycleCallHit {
	t.Helper()
	var hits []lifecycleCallHit
	for _, rel := range moduleLifecycleFiles(t, root) {
		hits = append(hits, findLifecycleOnStartCallHitsInFile(t, root, rel, forbidden)...)
	}
	return hits
}

func moduleLifecycleFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	for _, scanRoot := range []string{"internal/module", "internal/platform"} {
		absRoot := filepath.Join(root, filepath.FromSlash(scanRoot))
		if _, err := os.Stat(absRoot); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				t.Fatalf("walk %s: %v", path, walkErr)
			}
			if d.IsDir() {
				return nil
			}
			if d.Name() != "module.go" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("rel %s: %v", path, err)
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", absRoot, err)
		}
	}
	sort.Strings(files)
	return files
}

func findLifecycleOnStartCallHitsInFile(t *testing.T, root, rel string, forbidden map[string]bool) []lifecycleCallHit {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	fileDecls := fileFuncDecls(file)
	var hits []lifecycleCallHit
	ast.Inspect(file, func(n ast.Node) bool {
		hits = append(hits, findOnStartCallsInNode(fset, rel, n, forbidden, fileDecls)...)
		return true
	})
	return hits
}

func fileFuncDecls(file *ast.File) map[string]*ast.FuncDecl {
	fileDecls := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil {
			fileDecls[fn.Name.Name] = fn
		}
	}
	return fileDecls
}

func findOnStartCallsInNode(fset *token.FileSet, rel string, n ast.Node, forbidden map[string]bool, fileDecls map[string]*ast.FuncDecl) []lifecycleCallHit {
	cl, ok := n.(*ast.CompositeLit)
	if !ok || !isFxHookComposite(cl) {
		return nil
	}
	return findOnStartCallsInComposite(fset, rel, cl, forbidden, fileDecls)
}

func findOnStartCallsInComposite(fset *token.FileSet, rel string, cl *ast.CompositeLit, forbidden map[string]bool, fileDecls map[string]*ast.FuncDecl) []lifecycleCallHit {
	var hits []lifecycleCallHit
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok || keyName(kv.Key) != "OnStart" {
			continue
		}
		fn, ok := kv.Value.(*ast.FuncLit)
		if !ok || fn.Body == nil {
			continue
		}
		hits = append(hits, findForbiddenCallsInNode(fset, rel, fn.Body, forbidden, fileDecls)...)
	}
	return hits
}

func findForbiddenCallsInNode(fset *token.FileSet, rel string, node ast.Node, forbidden map[string]bool, fileDecls map[string]*ast.FuncDecl) []lifecycleCallHit {
	var hits []lifecycleCallHit
	ast.Inspect(node, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(ce.Fun)
		if forbidden[name] {
			hits = append(hits, lifecycleCallHit{Path: rel, Line: fset.Position(ce.Pos()).Line, Call: name, Receiver: callReceiverName(ce.Fun)})
			return true
		}
		if helper, ok := fileDecls[name]; ok && helper.Body != nil {
			hits = append(hits, findForbiddenCallsInHelper(fset, rel, name, helper.Body, forbidden)...)
		}
		return true
	})
	return hits
}

func findForbiddenCallsInHelper(fset *token.FileSet, rel, helperName string, node ast.Node, forbidden map[string]bool) []lifecycleCallHit {
	var hits []lifecycleCallHit
	ast.Inspect(node, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(ce.Fun)
		if forbidden[name] {
			hits = append(hits, lifecycleCallHit{
				Path:     rel,
				Line:     fset.Position(ce.Pos()).Line,
				Call:     name + " (via " + helperName + ")",
				Receiver: callReceiverName(ce.Fun),
			})
		}
		return true
	})
	return hits
}

func isFxHookComposite(cl *ast.CompositeLit) bool {
	sel, ok := cl.Type.(*ast.SelectorExpr)
	return ok && selectorXName(sel) == "fx" && sel.Sel.Name == "Hook"
}

func selectorXName(sel *ast.SelectorExpr) string {
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

func keyName(expr ast.Expr) string {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}
