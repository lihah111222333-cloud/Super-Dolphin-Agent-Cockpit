package memory

import (
	"log/slog"
	"testing"

	"github.com/kelindar/event"
)

// testHooksOption 是 MemoryLifecycleHooks 测试构造的函数式选项。
//
// 命名约定：选项函数 / 构造函数全部小写，故意不导出。它们是 _test.go 内部
// DSL，不应出现在生产代码或 godoc 中。Go 社区通常用大写 With 前缀，但那
// 是公开 API 风格；本 helper 是测试专属，保持小写以避免污染包外可见性。
type testHooksOption func(*MemoryLifecycleHooks)

func withEnabled(b bool) testHooksOption {
	return func(h *MemoryLifecycleHooks) { h.enabled = b }
}

func withLogger(l *slog.Logger) testHooksOption {
	return func(h *MemoryLifecycleHooks) { h.logger = l }
}

func withTeam(t *TeamMemoryManager) testHooksOption {
	return func(h *MemoryLifecycleHooks) { h.team = t }
}

// withTestCfg 同时塞 cfg 与从中派生的 rootDir / projectRoot /
// autoMemPathOverride，避免每个 fixture 重写一遍 5 个字段。
func withTestCfg(cfg *Config) testHooksOption {
	return func(h *MemoryLifecycleHooks) {
		h.cfg = cfg
		if cfg != nil {
			h.rootDir = cfg.RootDir
			h.projectRoot = cfg.ProjectRoot
			h.autoMemPathOverride = cfg.AutoMemPathOverride
		}
	}
}

// withLocks 覆盖默认 locks 实例。仅当测试需要预置 coordinator 状态
// （例如 markCrossScopeSameNameWarned）时使用。
func withLocks(c *diskLockCoordinator) testHooksOption {
	return func(h *MemoryLifecycleHooks) { h.locks = c }
}

// newTestGitProjectRoot 创建可被 AutoMem 解析的项目根目录。
// resolvedStoreRoot 现在会暴露 GetAutoMemPath 错误，测试不能再用非 git 临时目录伪装项目根。
func newTestGitProjectRoot(t *testing.T) string {
	t.Helper()
	projectRoot := t.TempDir()
	initGitRepoForMemoryTest(t, projectRoot)
	return projectRoot
}

// mustNewContextProvider 创建测试用 turn context provider，构造失败直接中止测试。
func mustNewContextProvider(t *testing.T, cfg *Config) *MemoryContextProvider {
	t.Helper()
	provider, err := NewContextProvider(cfg)
	if err != nil {
		t.Fatalf("NewContextProvider() error = %v", err)
	}
	return provider
}

func mustNewMemoryLifecycleHooks(t *testing.T, p memoryLifecycleHookParams) *MemoryLifecycleHooks {
	t.Helper()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	publisher, err := newMemoryIntentFailurePublisher(dispatcher)
	if err != nil {
		t.Fatalf("newMemoryIntentFailurePublisher() error = %v", err)
	}
	p.FailurePublisher = publisher
	hooks, err := NewMemoryLifecycleHooks(p)
	if err != nil {
		t.Fatalf("NewMemoryLifecycleHooks() error = %v", err)
	}
	return hooks
}

// newTestHooks 构造一个 locks 字段已就绪的 MemoryLifecycleHooks，
// 用于替代 _test.go 中的 &MemoryLifecycleHooks{...} 字面量。该字面量
// 在 _test.go 中被 archtest TestNoBareMemoryLifecycleHooksInTests 禁止：
// 历史上裸构造 + lazy locks 初始化曾导致 service.go:491 race 与 consolidator
// 共享实例被覆盖（修复见 memoryCoordinator 退化为纯 getter 的那次提交）。
//
// 不变量：返回的 hooks.locks 一定非 nil；若调用者通过 withConsolidator /
// withLocks 注入了 consolidator + 自定义 locks，最终 hooks.consolidator.locks
// 与 hooks.locks 指向同一实例，对齐 module.go provideMemoryLifecycleHooks
// 工厂里 `hooks.consolidator.locks = hooks.locks` 的 process-scoped 共享语义。
func newTestHooks(opts ...testHooksOption) *MemoryLifecycleHooks {
	h := &MemoryLifecycleHooks{
		locks: newDiskLockCoordinator(),
	}
	for _, opt := range opts {
		opt(h)
	}
	// Mirror module.go:272 — keep consolidator's coordinator pointing at
	// hooks.locks so cross-store dedupe / withDiskStoreLock observe the
	// same sync.Map state in tests as in production.
	if h.consolidator != nil {
		h.consolidator.locks = h.locks
	}
	return h
}
