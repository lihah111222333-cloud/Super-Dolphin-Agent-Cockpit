package prompt

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/contract/contracttest"
	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	platformsqlite "github.com/anthropic-ai/super-agent-v3/internal/platform/db/sqlite"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func registerPromptGoroutineCleanup(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s goroutines did not stop", label)
		}
	})
}

// TestServiceInvalidateSectionsIsConcurrentSafe pins the contract documented
// on contract.SectionInvalidator for the in-tree prompt.Service
// implementation: callers fan out from background goroutines (auto-dream,
// extractor, turn-tracking) without external synchronization, so
// InvalidateSections must be race-free and the generation counter must
// advance monotonically.
//
// The 16 writers × 200 invalidations stress loop lives in
// contracttest.SectionInvalidatorConcurrent so any future SectionInvalidator
// implementation can opt into the same conformance check.
func TestServiceInvalidateSectionsIsConcurrentSafe(t *testing.T) {
	contracttest.SectionInvalidatorConcurrent(t, func() contract.SectionInvalidator {
		svc := NewService(&Config{}, nil)
		// Prime the cache so InvalidateSections has entries to drop and
		// concurrent readers exist on the flight singleflight path.
		if _, err := svc.AssembleStart(context.Background(), StartInput{
			Provider: "claudecli",
			CWD:      t.TempDir(),
			Language: "English",
		}); err != nil {
			t.Fatalf("AssembleStart() error = %v", err)
		}
		return svc
	})
}

// TestCommitPromptIntentDraft_ConcurrentSubmit 验证两个 goroutine 同时提交同批次的两个草稿时，
// 最终只有一个草稿变为 enabled，不会出现两个都被拒绝的竞态。
// 依赖 SQLite IMMEDIATE 事务串行化：WithTx 使用 BEGIN IMMEDIATE，写锁在事务开始时即获取，
// 保证并发提交者只有一个能持有锁执行完整的"设为 enabled + 拒绝兄弟"序列。
func TestCommitPromptIntentDraft_ConcurrentSubmit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "concurrent-commit.db")
	dbA := openConcurrentCommitDB(t, ctx, dbPath, true)
	dbB := openConcurrentCommitDB(t, ctx, dbPath, false)
	storeA := promptstore.NewStoreWithDB(dbA, sqlc.New(dbA))
	storeB := promptstore.NewStoreWithDB(dbB, sqlc.New(dbB))

	rawInput := "concurrent-submit-raw-input"
	originHash := "concurrent-submit-origin-hash"
	expertCard := readyExpertIntentCard()
	expertCard.Title = "Concurrent Expert A"
	cardA, err := json.Marshal(expertCard)
	require.NoError(t, err)
	expertCard.Title = "Concurrent Expert B"
	expertCard.Workflow = []string{"Step one B", "Step two B"}
	cardB, err := json.Marshal(expertCard)
	require.NoError(t, err)

	draftA := promptIntentDraftForTest("intent/expert/concurrent-a", "/repo/concurrent", "expert", "ready_to_save", cardA, nil)
	draftA.RawInput, draftA.OriginHash = rawInput, originHash
	draftB := promptIntentDraftForTest("intent/expert/concurrent-b", "/repo/concurrent", "expert", "ready_to_save", cardB, nil)
	draftB.RawInput, draftB.OriginHash = rawInput, originHash
	_, err = storeA.UpsertIntentDraft(ctx, draftA)
	require.NoError(t, err)
	_, err = storeA.UpsertIntentDraft(ctx, draftB)
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	workersDone := make(chan struct{})
	registerPromptGoroutineCleanup(t, workersDone, "prompt intent commit")
	wg.Go(func() {
		<-start
		_, errs[0] = promptintent.HandleCommit(ctx, promptIntentStoreForTest(storeA), nil, nil, promptintent.CommitParams{DraftKey: "intent/expert/concurrent-a", Cwd: "/repo/concurrent"})
	})
	wg.Go(func() {
		<-start
		_, errs[1] = promptintent.HandleCommit(ctx, promptIntentStoreForTest(storeB), nil, nil, promptintent.CommitParams{DraftKey: "intent/expert/concurrent-b", Cwd: "/repo/concurrent"})
	})
	close(start)
	wg.Wait()
	close(workersDone)

	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	// 至少一个提交必须成功；IMMEDIATE 事务串行化保证另一个要么先成功要么看到兄弟草稿已被拒绝。
	require.GreaterOrEqual(t, successCount, 1, "errs: %v | %v", errs[0], errs[1])

	// 注：ListIntentDrafts SQL 用 (?2 IS NULL OR status=?2)，空字符串不是 NULL，必须显式过滤。
	drafts, err := storeA.ListIntentDrafts(ctx, promptstore.PromptIntentDraftListFilter{CWD: "/repo/concurrent", Status: "enabled", Limit: 10})
	require.NoError(t, err)
	require.Equal(t, 1, len(drafts), "同批次并发提交后应只有 1 个 enabled 草稿，drafts: %+v", drafts)
}

func openConcurrentCommitDB(t *testing.T, ctx context.Context, path string, migrate bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	for _, p := range []string{"PRAGMA foreign_keys = ON", "PRAGMA journal_mode = WAL", "PRAGMA busy_timeout = 5000", "PRAGMA synchronous = FULL"} {
		_, err := db.ExecContext(ctx, p)
		require.NoError(t, err)
	}
	if migrate {
		migrationsDir := filepath.Join(promptIntentCommitRepoRoot(t), "internal", "platform", "db", "sqlite", "migrations")
		require.NoError(t, platformsqlite.RunMigrations(ctx, db, migrationsDir))
	}
	return db
}
