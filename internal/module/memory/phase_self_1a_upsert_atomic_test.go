package memory

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// 本组测试锁定 diskStore.UpsertStructured / upsertWrite 的原子写入边界。
//
// 回归风险来自“先 Create、失败后 Update”的两段锁路径：
//
//  1. CreateStructured 加锁后检查不存在并写入；若已存在则释放锁并返回 ErrMemoryAlreadyExists。
//  2. 调用方收到 AlreadyExists 后再走 UpdateStructured，第二次加锁并覆盖当前文件内容。
//
// 两次锁之间其他 writer 可能已经更新文件，因此 UpsertStructured 必须把准备、加锁和写入收敛为一次临界区。

func TestPhaseSelf1a_UpsertStructuredCreatesNewEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore() error = %v", err)
	}
	req := MemoryWriteRequest{
		Name:        "Phase self.1a create",
		Description: "fresh entry created via Upsert",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: drive create path.\nHow to apply: when nothing exists.",
	}
	written, err := store.UpsertStructured(req)
	if err != nil {
		t.Fatalf("UpsertStructured() error = %v", err)
	}
	if written.Frontmatter.Name != req.Name {
		t.Fatalf("Upsert returned name=%q, want %q", written.Frontmatter.Name, req.Name)
	}
	if written.FilePath == "" {
		t.Fatal("Upsert returned empty FilePath")
	}
	if _, err := os.Stat(written.FilePath); err != nil {
		t.Fatalf("written file %q missing: %v", written.FilePath, err)
	}
}

func TestPhaseSelf1a_UpsertStructuredOverwritesExistingEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore() error = %v", err)
	}
	name := "Phase self.1a overwrite"
	initialReq := MemoryWriteRequest{
		Name:        name,
		Description: "initial content",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: initial.\nHow to apply: write once.",
	}
	if _, err := store.CreateStructured(initialReq); err != nil {
		t.Fatalf("CreateStructured(initial) error = %v", err)
	}
	// 已存在条目必须直接在 upsert 临界区内覆盖，不能把 ErrMemoryAlreadyExists 泄露给调用方。
	overwriteReq := MemoryWriteRequest{
		Name:        name,
		Description: "overwritten content",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: overwrite.\nHow to apply: replace content via Upsert.",
	}
	written, err := store.UpsertStructured(overwriteReq)
	if err != nil {
		t.Fatalf("UpsertStructured(overwrite) error = %v (must NOT return ErrMemoryAlreadyExists)", err)
	}
	if errors.Is(err, ErrMemoryAlreadyExists) {
		t.Fatal("Upsert wrongly returned ErrMemoryAlreadyExists; should overwrite atomically")
	}
	if !strings.Contains(written.Content, "Why: overwrite.") {
		t.Fatalf("Upsert did not overwrite content; got: %q", written.Content)
	}
	// 直接读取磁盘原文，确认覆盖发生在持久化层而不是解析层缓存里。
	raw, err := os.ReadFile(written.FilePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", written.FilePath, err)
	}
	if !strings.Contains(string(raw), "overwrite") {
		t.Fatalf("disk content not overwritten; got: %q", string(raw))
	}
	if strings.Contains(string(raw), "Why: initial.") {
		t.Fatalf("disk still contains initial content; overwrite incomplete:\n%s", string(raw))
	}
}

// 并发 upsert 测试锁定同名条目的核心写入不变量。
// 多个 goroutine 写同名条目时，所有调用都必须成功，不能泄露 ErrMemoryAlreadyExists；
// 最终磁盘内容允许最后写入者获胜，但不能出现部分写入、空内容或条目丢失。
func TestPhaseSelf1a_UpsertStructuredConcurrentRaceFree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := newDiskStore(root, nil)
	if err != nil {
		t.Fatalf("newDiskStore() error = %v", err)
	}
	const name = "Phase self.1a concurrent"
	concurrency, rounds := concurrentUpsertLoad()
	counters := runConcurrentUpserts(t, store, name, concurrency, rounds)
	assertConcurrentUpsertCounters(t, counters, concurrency*rounds)
	assertConcurrentUpsertFinalContent(t, store, name)
}

func concurrentUpsertLoad() (concurrency, rounds int) {
	if testing.Short() {
		return 8, 10
	}
	return 16, 50
}

type upsertRaceCounters struct {
	alreadyExists atomic.Int64
	otherErrors   atomic.Int64
	successWrites atomic.Int64
}

func runConcurrentUpserts(t *testing.T, store *diskStore, name string, concurrency, rounds int) *upsertRaceCounters {
	t.Helper()
	counters := &upsertRaceCounters{}
	var wg sync.WaitGroup
	wg.Add(concurrency)
	workersDone := make(chan struct{})
	registerMemoryGoroutineCleanup(t, workersDone, "memory upsert race")
	for i := range concurrency {
		go upsertRaceWorker(t, store, name, i, rounds, counters, &wg)
	}
	wg.Wait()
	close(workersDone)
	return counters
}

func upsertRaceWorker(t *testing.T, store *diskStore, name string, workerID, rounds int, counters *upsertRaceCounters, wg *sync.WaitGroup) {
	defer wg.Done()
	for r := range rounds {
		req := upsertRaceRequest(name, workerID, r)
		_, err := store.UpsertStructured(req)
		switch {
		case err == nil:
			counters.successWrites.Add(1)
		case errors.Is(err, ErrMemoryAlreadyExists):
			counters.alreadyExists.Add(1)
		default:
			counters.otherErrors.Add(1)
			t.Errorf("worker %d round %d: UpsertStructured() unexpected error: %v", workerID, r, err)
			return
		}
	}
}

func upsertRaceRequest(name string, workerID, round int) MemoryWriteRequest {
	content := fmt.Sprintf("rule worker=%d round=%d\nWhy: race exercise.\nHow to apply: concurrent upsert.", workerID, round)
	return MemoryWriteRequest{
		Name:        name,
		Description: "concurrent fixture",
		Type:        MemoryTypeFeedback,
		Body:        content,
	}
}

func assertConcurrentUpsertCounters(t *testing.T, counters *upsertRaceCounters, wantSuccess int) {
	t.Helper()
	if counters.otherErrors.Load() != 0 {
		t.Fatalf("got %d unexpected errors", counters.otherErrors.Load())
	}
	if counters.alreadyExists.Load() != 0 {
		t.Fatalf("Upsert leaked ErrMemoryAlreadyExists in %d calls (legacy Create-then-Update regression — Phase 自有.1a invariant violated)", counters.alreadyExists.Load())
	}
	if got, want := counters.successWrites.Load(), int64(wantSuccess); got != want {
		t.Fatalf("expected %d successful writes, got %d", want, got)
	}
}

func assertConcurrentUpsertFinalContent(t *testing.T, store *diskStore, name string) {
	t.Helper()
	final, err := store.Read(name)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if strings.TrimSpace(final.Content) == "" {
		t.Fatal("final content is empty")
	}
	raw, err := os.ReadFile(final.FilePath)
	if err != nil {
		t.Fatalf("ReadFile(final) error = %v", err)
	}
	if !strings.Contains(string(raw), "Why: race exercise.") {
		t.Fatalf("final content lost the writer marker (data corruption?):\n%s", string(raw))
	}
}
