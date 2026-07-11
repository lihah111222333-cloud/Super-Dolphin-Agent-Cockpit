package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	retrieval "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/retrieval"
)

// 本文件集中放置 memory baseline 测试共用 helper。
// invalidation 与 team 两组测试共享这些断言，集中维护可避免某个测试文件重命名或拆分时破坏跨文件调用。
//
// 两类 stub 的断言边界不同：
//   - UI RPC mutation 路径使用 recordingSectionInvalidator，保留完整调用切片；
//     assertRecordedInvalidation 要求调用方在执行前清空 rec.calls，且最终恰好一次命中。
//   - Consolidation 路径使用 sectionInvalidatorStub，只保留最后一次 reason/names；
//     consolidator 单次运行可能多次 invalidate，因此这些测试只能断言最后快照覆盖期望 section。
//
// 两类 stub 的 wire 形状都是 reason + section names，差异只在保留一次还是保留全量调用。

func sectionSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

func writeMemoryIndexFixture(t *testing.T, root string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(memory root) error = %v", err)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(memoryIndexPath(root), []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(MEMORY.md) error = %v", err)
	}
}

// assertRecordedInvalidation 校验 UI RPC stub 的 exact-once 约束。
// 调用方必须在执行被测路径前清空 rec.calls；helper 只接受恰好一次 reason 匹配且 names 覆盖期望 section。
// 这样预热或 setup 里的历史 invalidation 不会掩盖被测路径缺失通知的问题。
func assertRecordedInvalidation(
	t *testing.T,
	rec *recordingSectionInvalidator,
	when string,
	wantReason contract.InvalidateReason,
	wantSections ...string,
) {
	t.Helper()
	rec.mu.Lock()
	calls := append([]recordedInvalidateCall(nil), rec.calls...)
	rec.mu.Unlock()
	matchCount := 0
	for _, call := range calls {
		if call.reason != wantReason {
			continue
		}
		got := sectionSet(call.names)
		all := true
		for _, want := range wantSections {
			if _, ok := got[want]; !ok {
				all = false
				break
			}
		}
		if all {
			matchCount++
		}
	}
	if matchCount != 1 {
		t.Fatalf("%s: matchCount=%d, want exactly 1; reason=%q sections⊇%v; calls=%#v",
			when, matchCount, wantReason, wantSections, calls)
	}
}

func newPhase4UIDeps(t *testing.T) (memoryHandlerDeps, string, string) {
	t.Helper()
	projectRoot := newTestGitProjectRoot(t)
	privateRoot := filepath.Join(t.TempDir(), "private")
	cfg := &Config{
		Enabled:             true,
		EnableTools:         true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: privateRoot,
	}
	deps := memoryHandlerDeps{
		Service:  newServiceWithConsolidator(cfg, nil, nil, nil),
		Sections: &recordingSectionInvalidator{},
	}
	return deps, projectRoot, privateRoot
}

// findEntriesByName 返回 frontmatter Name 精确匹配的 manifest entries。
// 测试只关心目标条目存在，不绑定 BuildManifest 的完整条目数量，因为 index 等辅助文件也可能进入 manifest。
func findEntriesByName(entries []retrieval.MemoryEntry, name string) []retrieval.MemoryEntry {
	out := make([]retrieval.MemoryEntry, 0, 1)
	for _, e := range entries {
		if e.Frontmatter.Name == name {
			out = append(out, e)
		}
	}
	return out
}
