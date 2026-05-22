package prompt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Phase 2.3a: Invalidate × Assemble 并发交叉测试.
//
// 锁定的契约（assembler.go / cache.go / user_context_builder.go 现有实现满足，
// 本组测试防回归）：
//
//  1. AssembleStart 与 InvalidateSections 并发 race-free（go test -race 验证）
//  2. cache.Generation() 在并发 reader 干扰下严格单调递增
//  3. singleflight 中途 invalidate：旧 generation 桶被 store 后，新 generation
//     lookup 不会命中旧 entry → 重新 compute → 看到 fresh value（关键 race
//     scenario，p24 Phase 2.0.1 / 2.1.AB 多次审查反复提到的缺口）
//  4. Invalidate 一次同时 reset prompt.cache 和 userContextCache（双 cache
//     协调不变量；防未来重构漏掉一份导致 stale userContext）
//
// 历史背景：p25 Phase 4.0a spike 子项 1 已确认所有 prompt-relevant 写入点都接
// invalidateMemorySections，所以本组测试聚焦于 invalidate 与 assemble 在并发下
// 的协调，而不是 invalidate 链路覆盖度。

func TestPhase2_3aConcurrentAssembleStartInvalidateRaceFree(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	internal := svc.(*service)
	cwd := t.TempDir()
	// Prime the cache so InvalidateSections has entries to drop and concurrent
	// readers exercise the flight singleflight path.
	if _, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
		Language: "English",
	}); err != nil {
		t.Fatalf("priming AssembleStart() error = %v", err)
	}

	const (
		readers = 8
		writers = 4
		rounds  = 200
	)
	var wg sync.WaitGroup
	wg.Add(readers + writers)

	for i := 0; i < readers; i++ {
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for r := 0; r < rounds; r++ {
				assembly, err := svc.AssembleStart(ctx, StartInput{
					Provider: "claudecli",
					CWD:      cwd,
					Language: "English",
				})
				if err != nil {
					t.Errorf("AssembleStart() error = %v", err)
					return
				}
				// Snapshot generation must be monotonic across the run; we
				// can't pin a specific value here because writers race in
				// parallel, but >=0 (never panics / returns garbage) is a
				// minimal safety check.
				if assembly.Snapshot.Generation > internal.cache.Generation() {
					t.Errorf("snapshot generation %d > current cache generation %d",
						assembly.Snapshot.Generation, internal.cache.Generation())
					return
				}
			}
		}()
	}
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				internal.InvalidateSections(contract.InvalidateMemoryWrite, contract.DynamicSectionMemory)
			}
		}()
	}
	wg.Wait()

	// Sanity: writers performed writers*rounds invalidations; generation must
	// have advanced at least that many times (could be higher if priming or
	// section computation triggered extra invalidations).
	finalGen := internal.cache.Generation()
	if finalGen < uint64(writers*rounds) {
		t.Errorf("final generation = %d, want at least %d (writers*rounds)", finalGen, writers*rounds)
	}
}

func TestPhase2_3aGenerationMonotonicUnderConcurrentReaders(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	internal := svc.(*service)
	cwd := t.TempDir()

	const (
		readers       = 8
		invalidations = 100
	)
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	rwg.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer rwg.Done()
			ctx := context.Background()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = svc.AssembleStart(ctx, StartInput{
					Provider: "claudecli",
					CWD:      cwd,
					Language: "English",
				})
			}
		}()
	}

	prev := internal.cache.Generation()
	for i := 0; i < invalidations; i++ {
		if err := svc.Invalidate(context.Background(), contract.InvalidateMemoryWrite); err != nil {
			t.Fatalf("Invalidate() error = %v", err)
		}
		curr := internal.cache.Generation()
		if curr <= prev {
			t.Fatalf("generation regressed under concurrent readers: prev=%d curr=%d (invalidation #%d)",
				prev, curr, i)
		}
		prev = curr
	}
	close(stop)
	rwg.Wait()
}

// TestPhase2_3aSingleflightInvalidateNoStaleStore — Phase 2.3 关键 race
// scenario:
//
//  1. AssembleStart 触发自定义 section Compute（带 sleep）— singleflight 持
//     有 (generation_v1, cacheKey) 锁
//  2. compute sleep 期间，invalidate 触发 generation+1（→ generation_v2）
//  3. compute 完成，s.cache.Store(cacheKey, generation_v1, value=v1) 写到
//     旧 generation 桶
//  4. 后续 AssembleStart 用 generation_v2 lookup → 旧桶 miss → 重新 compute
//     → 看到 fresh value v2
//
// 验证：v1 不会被新 generation 命中复用（防 stale store 污染）；v2 是 fresh
// compute 的结果（防 invalidate 后第二次 assemble 仍返回旧值）。
func TestPhase2_3aSingleflightInvalidateNoStaleStore(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	var counter atomic.Int64
	gate := newPhase2SingleflightGate(&counter)
	const sectionName = "phase2_3a_test_counter"
	if err := svc.RegisterSection(PromptSection{
		Name:    sectionName,
		Order:   10000,
		Region:  PromptRegionStatic,
		Compute: gate.compute,
	}); err != nil {
		t.Fatalf("RegisterSection() error = %v", err)
	}
	cwd := t.TempDir()

	resultCh := make(chan phase2AssembleResult, 1)
	go func() {
		assembly, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      cwd,
			Language: "English",
		})
		resultCh <- phase2AssembleResult{assembly, err}
	}()

	// Step 2: 等自定义 compute 已经在旧 generation 下进入 singleflight，
	// 然后在 store 触发前 invalidate。
	waitForPhase2SingleflightComputeStart(t, gate, resultCh)
	if err := svc.Invalidate(context.Background(), contract.InvalidateMemoryWrite); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	gate.release()

	// Step 3: 等第一次 AssembleStart 完成（store 到旧 generation 桶）.
	first := <-resultCh
	if first.err != nil {
		t.Fatalf("first AssembleStart() error = %v", first.err)
	}
	if !strings.Contains(first.assembly.BaseInstructions, "counter=1") {
		t.Fatalf("first assembly missing counter=1: %q", first.assembly.BaseInstructions)
	}

	// Step 4: 第二次 AssembleStart 用新 generation lookup → miss → fresh
	// compute → counter=2.
	second, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
		Language: "English",
	})
	if err != nil {
		t.Fatalf("second AssembleStart() error = %v", err)
	}
	if !strings.Contains(second.BaseInstructions, "counter=2") {
		t.Fatalf("second assembly should re-compute fresh after invalidate, got: %q",
			second.BaseInstructions)
	}
	// Counter-baseline: 第三次 AssembleStart（无 invalidate）应命中 cache 复用
	// counter=2，不会 compute counter=3.
	third, err := svc.AssembleStart(context.Background(), StartInput{
		Provider: "claudecli",
		CWD:      cwd,
		Language: "English",
	})
	if err != nil {
		t.Fatalf("third AssembleStart() error = %v", err)
	}
	if !strings.Contains(third.BaseInstructions, "counter=2") {
		t.Fatalf("third assembly should reuse cached counter=2 (no invalidate between #2 and #3), got: %q",
			third.BaseInstructions)
	}
}

type phase2AssembleResult struct {
	assembly StartAssembly
	err      error
}

type phase2SingleflightGate struct {
	counter *atomic.Int64
	started chan struct{}
	done    chan struct{}
}

func newPhase2SingleflightGate(counter *atomic.Int64) *phase2SingleflightGate {
	return &phase2SingleflightGate{
		counter: counter,
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

func (g *phase2SingleflightGate) compute(ctx context.Context, _ SectionContext) (*string, error) {
	call := g.counter.Add(1)
	if call == 1 {
		close(g.started)
		select {
		case <-g.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s := fmt.Sprintf("counter=%d", call)
	return &s, nil
}

func (g *phase2SingleflightGate) release() {
	close(g.done)
}

func waitForPhase2SingleflightComputeStart(
	t *testing.T,
	gate *phase2SingleflightGate,
	resultCh <-chan phase2AssembleResult,
) {
	t.Helper()
	select {
	case <-gate.started:
	case first := <-resultCh:
		t.Fatalf("first AssembleStart() finished before test section entered compute: err=%v assembly=%q",
			first.err, first.assembly.BaseInstructions)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for test section compute to start")
	}
}

// TestPhase2_3aInvalidateRoutingContract — 两路 invalidate 路由契约：
//
//   - `service.Invalidate(ctx, reason)` (assembler.go:137) = 全清模式：同时
//     advance prompt.cache 和 userContextCache 的 generation。防御性 reset。
//
//   - `service.InvalidateSections(reason, names...)` (invalidation.go:15) =
//     section-fine-grained：仅 advance prompt.cache。userContextCache **不动**
//     —— 它是 content-aware sources-keyed（baseUserContextCacheKey 用
//     sourceDigest 作为 key 的一部分），source content 变化时 cacheKey 自动
//     变化 → lookup miss → 重新 compute。不需要靠 generation invalidate。
//
// 本测试守住这个分工。如果未来重构让 InvalidateSections 也去清
// userContextCache.Generation，需先评估是否使 content-aware 机制冗余；本
// 测试 fail 是重构意图的信号，不是 bug。
func TestPhase2_3aInvalidateRoutingContract(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	internal := svc.(*service)

	// 契约 1: Invalidate(ctx, reason) 同时 advance 两个 cache。
	gen0Cache := internal.cache.Generation()
	gen0User := internal.userContextCache.Generation()
	if err := svc.Invalidate(context.Background(), contract.InvalidateMemoryWrite); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	if got := internal.cache.Generation(); got <= gen0Cache {
		t.Fatalf("Invalidate(): prompt.cache did not advance: prev=%d got=%d", gen0Cache, got)
	}
	if got := internal.userContextCache.Generation(); got <= gen0User {
		t.Fatalf("Invalidate(): userContextCache did not advance: prev=%d got=%d", gen0User, got)
	}

	// 契约 2: InvalidateSections(reason, names...) 仅 advance prompt.cache，
	// userContextCache 保持不动（content-aware，靠 sourceDigest 变化驱动
	// cacheKey miss）。
	gen1Cache := internal.cache.Generation()
	gen1User := internal.userContextCache.Generation()
	internal.InvalidateSections(contract.InvalidateMemoryWrite, contract.DynamicSectionMemory)
	if got := internal.cache.Generation(); got <= gen1Cache {
		t.Fatalf("InvalidateSections(): prompt.cache did not advance: prev=%d got=%d", gen1Cache, got)
	}
	if got := internal.userContextCache.Generation(); got != gen1User {
		t.Fatalf("InvalidateSections(): userContextCache should NOT advance (sources-keyed via sourceDigest, not sections-keyed): prev=%d got=%d", gen1User, got)
	}
}
