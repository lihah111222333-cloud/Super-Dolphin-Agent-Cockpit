package memory

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// 本文件锁定 UI RPC 写入路径的 prompt-section invalidation 行为。
// 每个持久化写入口都必须触发 Memory、MemoryContext 和 MemoryEntrypoint 失效；
// 若未来改动漏掉 invalidate 调用，这组测试应在同次变更中失败。
//
// 断言约定：reason 必须是 InvalidateMemoryWrite，names 覆盖期望 section，
// 并且每个持久化路径只记录一次失效事件。

//
// 辅助函数集中在同包 helper 测试文件，保持本文件只呈现失效行为断言。

func TestPhase4BaselineUpsertUIMemoryEntryInvalidatesDurableSections(t *testing.T) {
	deps, projectRoot, _ := newPhase4UIDeps(t)
	rec := deps.Sections.(*recordingSectionInvalidator)

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Phase4 baseline create",
		Description: "create-side invalidation guard",
		Type:        "reference",
		Content:     "Lock down create-time invalidation as a Phase 4.0 baseline.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create) error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after upsert(create)",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)

	// 清空创建路径记录，只断言 update 路径本身也会触发失效。
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if _, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:          projectRoot,
		Target:       "private",
		ExistingPath: created.Path,
		Name:         "Phase4 baseline create",
		Description:  "update-side invalidation guard",
		Type:         "reference",
		Content:      "Update path also must invalidate.",
	}); err != nil {
		t.Fatalf("upsertUIMemoryEntry(update) error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after upsert(update)",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}

func TestPhase4BaselineDeleteUIMemoryEntryInvalidatesDurableSections(t *testing.T) {
	deps, projectRoot, _ := newPhase4UIDeps(t)
	rec := deps.Sections.(*recordingSectionInvalidator)

	created, err := upsertUIMemoryEntry(context.Background(), deps, uiMemoryEntryUpsertParams{
		CWD:         projectRoot,
		Target:      "private",
		Name:        "Phase4 baseline delete fixture",
		Description: "fixture for delete-time guard",
		Type:        "reference",
		Content:     "Will be deleted.",
	})
	if err != nil {
		t.Fatalf("upsertUIMemoryEntry(create fixture) error = %v", err)
	}

	// 清空创建 fixture 的记录，只观察 delete 路径发出的失效信号。
	rec.mu.Lock()
	rec.calls = nil
	rec.mu.Unlock()

	if err := deleteUIMemoryEntry(context.Background(), deps, uiMemoryEntryDeleteParams{
		CWD:    projectRoot,
		Target: "private",
		Path:   created.Path,
	}); err != nil {
		t.Fatalf("deleteUIMemoryEntry() error = %v", err)
	}
	assertRecordedInvalidation(t, rec, "after delete",
		contract.InvalidateMemoryWrite,
		contract.DynamicSectionMemory,
		contract.DynamicSectionMemoryContext,
		contract.DynamicSectionMemoryEntrypoint,
	)
}
