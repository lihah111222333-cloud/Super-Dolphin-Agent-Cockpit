package search

import (
	"sync"
	"testing"
)

// TestFileutilDescriptorLookupsConcurrent 验证并发搜索请求下描述符辅助函数保持行为稳定。
func TestFileutilDescriptorLookupsConcurrent(t *testing.T) {
	t.Parallel()

	const workers = 16
	const iterations = 100
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			for iteration := 0; iteration < iterations; iteration++ {
				if got := inferLanguage("main.go"); got != "go" {
					t.Errorf("inferLanguage(main.go) = %q, want go", got)
				}
				if got := normalizeLanguageAlias(" GO "); got != "go" {
					t.Errorf("normalizeLanguageAlias(GO) = %q, want go", got)
				}
				if !shouldSkipDir(" .CACHE ") {
					t.Error("shouldSkipDir(.CACHE) = false, want true")
				}
				if !shouldExcludePath("/workspace/tmp/cache/result.txt") {
					t.Error("shouldExcludePath(cache path) = false, want true")
				}
			}
		})
	}
	wg.Wait()
}
