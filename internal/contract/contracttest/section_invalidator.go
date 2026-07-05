// Package contracttest 提供 contract 级不变量测试工具。
// 下游实现可复用这些 helper 校验并发安全和 generation 单调性，不需要各自重写同一套测试。
package contracttest

import (
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// SectionInvalidatorConcurrent 并发压测 contract.SectionInvalidator。
// factory 每次必须返回新的可用实例；需要预热缓存的实现应在 factory 内完成预热。
// 在 `-race` 下可暴露缓存或 provider map 锁遗漏；普通测试也会校验不 panic 和 generation 单调递增。
func SectionInvalidatorConcurrent(t *testing.T, factory func() contract.SectionInvalidator) {
	t.Helper()
	inv := factory()
	if inv == nil {
		t.Fatal("contracttest.SectionInvalidatorConcurrent: factory returned nil SectionInvalidator")
	}

	const (
		writers   = 16
		perWriter = 200
	)
	sectionNames := []string{
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryEntrypoint,
		contract.DynamicSectionMemoryContext,
	}

	ctx := t.Context()
	var wg sync.WaitGroup
	for i := range writers {
		seed := i
		wg.Go(func() {
			for j := range perWriter {
				select {
				case <-ctx.Done():
					return
				default:
				}
				name := sectionNames[(seed+j)%len(sectionNames)]
				_ = inv.InvalidateSections(contract.InvalidateMemoryWrite, name)
			}
		})
	}
	wg.Wait()

	gen := inv.InvalidateSections(contract.InvalidateMemoryWrite, contract.DynamicSectionMemory)
	if gen == 0 {
		t.Fatalf("InvalidateSections() final generation = 0; expected monotonically increasing counter")
	}
}
