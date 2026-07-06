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

// Invalidate × Assemble 并发交叉测试覆盖 prompt 缓存代际切换和 singleflight 竞态。
//
// 锁定的契约（assembler.go / cache.go / user_context_builder.go 现有实现满足，
// 本组测试防回归）：
//
//  1. AssembleStart 与 InvalidateSections 并发 race-free（go test -race 验证）
//  2. cache.Generation() 在并发 reader 干扰下严格单调递增
//  3. singleflight 中途 invalidate：旧 generation 桶被 store 后，新 generation
//     lookup 不会命中旧 entry → 重新 compute → 看到 fresh value（关键竞态场景）
//  4. Invalidate 一次同时 reset prompt.cache 和 userContextCache（双 cache
//     协调不变量；防未来重构漏掉一份导致 stale userContext）
//
// 本组测试聚焦 invalidate 与 assemble 在并发下的协调，不验证所有写入点是否接入失效链路。

func TestPhase2_3aConcurrentAssembleStartInvalidateRaceFree(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	internal := svc.(*service)
	input := phase2ConcurrentStartInput()
	// 先填充缓存，让 InvalidateSections 有可删除条目，并让并发 reader 覆盖 singleflight 路径。
	if _, err := svc.AssembleStart(context.Background(), input); err != nil {
		t.Fatalf("priming AssembleStart() error = %v", err)
	}

	const (
		readers = 8
		writers = 4
		rounds  = 200
	)
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	registerPromptGoroutineCleanup(t, workersDone, "phase2 invalidate race")

	for range readers {
		wg.Go(func() {
			ctx := context.Background()
			for range rounds {
				assembly, err := svc.AssembleStart(ctx, input)
				if err != nil {
					t.Errorf("AssembleStart() error = %v", err)
					return
				}
				// snapshot generation 只要求不超过当前 generation；writer 并发推进时不能固定具体值。
				// 这个检查同时防止 panic 或垃圾值越界。
				if assembly.Snapshot.Generation > internal.cache.Generation() {
					t.Errorf("snapshot generation %d > current cache generation %d",
						assembly.Snapshot.Generation, internal.cache.Generation())
					return
				}
			}
		})
	}
	for range writers {
		wg.Go(func() {
			for range rounds {
				internal.InvalidateSections(contract.InvalidateMemoryWrite, contract.DynamicSectionMemory)
			}
		})
	}
	wg.Wait()
	close(workersDone)

	// writer 共执行 writers*rounds 次失效，generation 至少应推进这么多次。
	// 预热或 section 计算触发额外失效时允许更高。
	finalGen := internal.cache.Generation()
	if finalGen < uint64(writers*rounds) {
		t.Errorf("final generation = %d, want at least %d (writers*rounds)", finalGen, writers*rounds)
	}
}

func TestPhase2_3aGenerationMonotonicUnderConcurrentReaders(t *testing.T) {
	t.Parallel()
	svc := NewService(&Config{}, nil)
	internal := svc.(*service)
	input := phase2ConcurrentStartInput()

	const (
		readers       = 8
		invalidations = 100
	)
	stop := make(chan struct{})
	var rwg sync.WaitGroup
	readersDone := make(chan struct{})
	registerPromptGoroutineCleanup(t, readersDone, "phase2 generation readers")
	for range readers {
		rwg.Go(func() {
			ctx := context.Background()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = svc.AssembleStart(ctx, input)
			}
		})
	}

	prev := internal.cache.Generation()
	for i := range invalidations {
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
	close(readersDone)
}

// phase2ConcurrentStartInput 避免并发压力测试在每轮 reader 中启动 git status 子进程。
// 这些测试只验证 section cache/invalidate 协调，SystemContext 有独立测试覆盖。
func phase2ConcurrentStartInput() StartInput {
	return StartInput{
		Provider: "claudecli",
		Language: "English",
	}
}

// 本测试锁定 singleflight 与 invalidate 的关键竞态：
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
	resultDone := make(chan struct{})
	var resultWG sync.WaitGroup
	registerPromptGoroutineCleanup(t, resultDone, "phase2 singleflight assemble")
	resultWG.Go(func() {
		defer close(resultDone)
		assembly, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      cwd,
			Language: "English",
		})
		resultCh <- phase2AssembleResult{assembly, err}
	})

	// 步骤 2：等自定义 compute 已经在旧 generation 下进入 singleflight，
	// 然后在 store 触发前执行 invalidate。
	waitForPhase2SingleflightComputeStart(t, gate, resultCh)
	if err := svc.Invalidate(context.Background(), contract.InvalidateMemoryWrite); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	gate.release()

	// 步骤 3：等第一次 AssembleStart 完成，结果应写入旧 generation 桶。
	first := <-resultCh
	resultWG.Wait()
	if first.err != nil {
		t.Fatalf("first AssembleStart() error = %v", first.err)
	}
	if !strings.Contains(first.assembly.BaseInstructions, "counter=1") {
		t.Fatalf("first assembly missing counter=1: %q", first.assembly.BaseInstructions)
	}

	// 步骤 4：第二次 AssembleStart 用新 generation lookup → miss，
	// 重新 compute 后应得到 counter=2。
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

// 本测试锁定两路 invalidate 路由契约：
//
//   - `service.Invalidate(ctx, reason)` (assembler.go:137) = 全清模式：同时
//     advance prompt.cache 和 userContextCache 的 generation。防御性 reset。
//
//   - 细粒度路径：`service.InvalidateSections(reason, names...)` (invalidation.go:15) =
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
