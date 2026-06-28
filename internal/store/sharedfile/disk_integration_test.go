package sharedfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"golang.org/x/sync/errgroup"
)

// 本文件验证 sharedfile 的磁盘正文与 DB 索引分离边界。
// 测试用内存 row store 承担 DB 侧，用 t.TempDir() 承担真实文件系统读写。

func newDiskBackedStore(t *testing.T) (*store, *fakeRowStore, sharedfilefs.Config) {
	t.Helper()
	dir := t.TempDir()
	cfg := sharedfilefs.Config{CWD: dir, InlineThresholdBytes: 1024}
	rows := newFakeRowStore()
	return &store{q: rows.querier(), cfg: cfg}, rows, cfg
}

func TestUpsert_SmallFile_StoresInlineAndOnDisk(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	got, err := s.Upsert(context.Background(), UpsertParams{
		Path:      "handoff/task-1/notes.md",
		Content:   "small body",
		UpdatedBy: "agent",
	})
	if err != nil {
		t.Fatalf("Upsert error = %v", err)
	}
	if got.Content != "small body" {
		t.Fatalf("returned content = %q, want small body", got.Content)
	}
	row, ok := rows.byPath["handoff/task-1/notes.md"]
	if !ok {
		t.Fatal("DB row missing")
	}
	if row.Content != "small body" {
		t.Fatalf("DB content = %q, want small body (inline under threshold)", row.Content)
	}
	if row.ContentLocation != contentLocationInline {
		t.Fatalf("DB content_location = %q, want %q", row.ContentLocation, contentLocationInline)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, "handoff/task-1/notes.md")
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("ReadFile err = %v", err)
	}
	if string(disk) != "small body" {
		t.Fatalf("disk content = %q, want small body", disk)
	}
}

func TestUpsert_LargeFile_DBHasNoBody(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	big := strings.Repeat("a", 2048) // > 1024 threshold
	got, err := s.Upsert(context.Background(), UpsertParams{
		Path:      "dag/dag-1/output.json",
		Content:   big,
		UpdatedBy: "agent",
	})
	if err != nil {
		t.Fatalf("Upsert error = %v", err)
	}
	// 大文件写入后 DB 行不存正文，返回值仍要回填调用方刚写入的内容。
	if got.Content != big {
		t.Fatalf("returned content len = %d, want %d", len(got.Content), len(big))
	}
	row := rows.byPath["dag/dag-1/output.json"]
	if row.Content != "" {
		t.Fatalf("DB content len = %d, want 0 (above threshold)", len(row.Content))
	}
	if row.ContentLocation != contentLocationDisk {
		t.Fatalf("DB content_location = %q, want %q", row.ContentLocation, contentLocationDisk)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, "dag/dag-1/output.json")
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("ReadFile err = %v", err)
	}
	if string(disk) != big {
		t.Fatalf("disk content len = %d, want %d", len(disk), len(big))
	}
}

func TestSharedFileUpsertDoesNotOverwriteOnDBFailure(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	path := "handoff/task-1/notes.md"
	if _, err := s.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "old body",
		UpdatedBy: "agent",
	}); err != nil {
		t.Fatalf("seed Upsert error = %v", err)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, path)
	rows.upsertErr = errors.New("db write failed")

	_, err := s.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   "new body",
		UpdatedBy: "agent",
	})
	if err == nil {
		t.Fatal("Upsert err = nil, want DB failure")
	}
	disk, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("ReadFile after failed Upsert err = %v", readErr)
	}
	if string(disk) != "old body" {
		t.Fatalf("disk content after failed Upsert = %q, want old body", string(disk))
	}
	matches, globErr := filepath.Glob(abs + ".*.tmp")
	if globErr != nil {
		t.Fatalf("glob temp files: %v", globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("orphan temp files after failed Upsert = %v", matches)
	}
}

func TestSharedFileUpsertRollsBackDBOnPublishFailure(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	path := "handoff/task-1/blob.txt"
	oldBody := strings.Repeat("o", 2048)
	if _, err := s.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   oldBody,
		UpdatedBy: "seed",
	}); err != nil {
		t.Fatalf("seed Upsert error = %v", err)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, path)
	dir := filepath.Dir(abs)
	makeDirReadOnlyDuringAgentUpsert(t, rows, path, dir, abs)

	_, err := s.Upsert(context.Background(), UpsertParams{
		Path:      path,
		Content:   strings.Repeat("n", 2048),
		UpdatedBy: "agent",
	})
	if err == nil {
		t.Fatal("Upsert err = nil, want publish failure")
	}
	restoreWritableDir(t, dir)
	assertSharedFilePreservedAfterPublishFailure(t, rows, path, abs, oldBody)
}

func TestSharedFileUpsertConcurrentSamePathDoesNotDeletePeerStaging(t *testing.T) {
	s, rows, cfg := newDiskBackedStore(t)

	path := "handoff/task-1/concurrent.txt"
	firstContent := strings.Repeat("1", 2048)
	secondContent := strings.Repeat("2", 2048)
	firstInDB := make(chan struct{})
	releaseFirst := make(chan struct{})
	rows.onUpsert = func(arg sqlc.UpsertSharedFileParams) {
		if arg.Path != path || arg.UpdatedBy != "writer-1" {
			return
		}
		close(firstInDB)
		<-releaseFirst
	}

	var group errgroup.Group
	group.Go(func() error {
		_, err := s.Upsert(context.Background(), UpsertParams{Path: path, Content: firstContent, UpdatedBy: "writer-1"})
		return err
	})
	<-firstInDB

	group.Go(func() error {
		_, err := s.Upsert(context.Background(), UpsertParams{Path: path, Content: secondContent, UpdatedBy: "writer-2"})
		return err
	})
	// Give the second writer a chance to reach the old cleanupStagedTemps path.
	// With per-path locking it will wait here until writer-1 is released.
	waitForWriter(t, rows, path, "writer-2", 100*time.Millisecond)
	close(releaseFirst)

	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent Upsert error = %v", err)
	}
	row := rows.row(path)
	if row.UpdatedBy != "writer-2" {
		t.Fatalf("final DB updated_by = %q, want writer-2; row=%+v", row.UpdatedBy, row)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, path)
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read final disk content: %v", err)
	}
	if string(disk) != secondContent {
		t.Fatalf("final disk content len=%d prefix=%q, want writer-2 content", len(disk), string(disk[:1]))
	}
}

func waitForWriter(t *testing.T, rows *fakeRowStore, path, updatedBy string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if rows.row(path).UpdatedBy == updatedBy {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func makeDirReadOnlyDuringAgentUpsert(t *testing.T, rows *fakeRowStore, path, dir, abs string) {
	t.Helper()
	rows.onUpsert = func(arg sqlc.UpsertSharedFileParams) {
		if arg.Path == path && arg.UpdatedBy == "agent" {
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatalf("chmod dir read-only: %v", err)
			}
		}
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o755)
		matches, _ := filepath.Glob(abs + ".*.tmp")
		for _, match := range matches {
			_ = os.Remove(match)
		}
	})
}

func restoreWritableDir(t *testing.T, dir string) {
	t.Helper()
	if chmodErr := os.Chmod(dir, 0o755); chmodErr != nil {
		t.Fatalf("restore dir permissions: %v", chmodErr)
	}
}

func assertSharedFilePreservedAfterPublishFailure(t *testing.T, rows *fakeRowStore, path, abs, oldBody string) {
	t.Helper()
	row := rows.byPath[path]
	if row.UpdatedBy != "seed" || row.ContentLocation != contentLocationDisk {
		t.Fatalf("DB row after publish failure = %+v, want rolled back seed disk row", row)
	}
	disk, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("ReadFile after failed publish err = %v", readErr)
	}
	if string(disk) != oldBody {
		t.Fatalf("disk content after failed publish changed; len=%d want %d", len(disk), len(oldBody))
	}
}

func TestGet_DiskHit_OverridesDBContent(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	// 预置大文件约定下的空 DB 正文，并在磁盘侧写入权威正文；Get 必须以磁盘正文为准。
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	rows.byPath["dag/dag-1/output.json"] = sqlc.SharedFile{
		Path: "dag/dag-1/output.json", Content: "", ContentLocation: contentLocationDisk, UpdatedBy: "agent",
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, "dag/dag-1/output.json")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir err = %v", err)
	}
	if err := os.WriteFile(abs, []byte("disk-canonical"), 0o644); err != nil {
		t.Fatalf("seed disk err = %v", err)
	}

	got, err := s.Get(context.Background(), "dag/dag-1/output.json")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got.Content != "disk-canonical" {
		t.Fatalf("Content = %q, want disk-canonical", got.Content)
	}
	// 元数据仍来自 DB 行，磁盘只承担正文来源。
	if got.UpdatedBy != "agent" {
		t.Fatalf("UpdatedBy = %q, want agent (from DB)", got.UpdatedBy)
	}
}

func TestGet_DiskMiss_FallsBackToDB(t *testing.T) {
	t.Parallel()
	s, rows, _ := newDiskBackedStore(t)

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	rows.byPath["handoff/legacy/note.md"] = sqlc.SharedFile{
		Path: "handoff/legacy/note.md", Content: "from-db-only", ContentLocation: contentLocationInline, UpdatedBy: "system",
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}

	got, err := s.Get(context.Background(), "handoff/legacy/note.md")
	if err != nil {
		t.Fatalf("Get err = %v", err)
	}
	if got.Content != "from-db-only" {
		t.Fatalf("Content = %q, want from-db-only", got.Content)
	}
}

func TestGet_DiskMissForDiskRow_ReturnsError(t *testing.T) {
	t.Parallel()
	s, rows, _ := newDiskBackedStore(t)

	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	rows.byPath["handoff/missing/blob.bin"] = sqlc.SharedFile{
		Path:            "handoff/missing/blob.bin",
		Content:         "",
		ContentLocation: contentLocationDisk,
		UpdatedBy:       "system",
		CreatedAt:       now.UnixMilli(),
		UpdatedAt:       now.UnixMilli(),
	}

	_, err := s.Get(context.Background(), "handoff/missing/blob.bin")
	if err == nil {
		t.Fatal("Get err = nil, want missing disk content error")
	}
	if !strings.Contains(err.Error(), "disk content") {
		t.Fatalf("Get err = %v, want disk content context", err)
	}
}

func TestGet_DiskMissAndDBMiss_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	s, _, _ := newDiskBackedStore(t)

	_, err := s.Get(context.Background(), "handoff/never-existed.md")
	if err == nil {
		t.Fatal("Get err = nil, want not found")
	}
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("err = %v, want platformdb.ErrNotFound", err)
	}
}

func TestDelete_RemovesBothLayers(t *testing.T) {
	t.Parallel()
	s, _, cfg := newDiskBackedStore(t)

	if _, err := s.Upsert(context.Background(), UpsertParams{
		Path: "handoff/task-1/x.md", Content: "body", UpdatedBy: "agent",
	}); err != nil {
		t.Fatalf("Upsert err = %v", err)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, "handoff/task-1/x.md")
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("disk file should exist after Upsert: %v", err)
	}

	count, err := s.Delete(context.Background(), "handoff/task-1/x.md")
	if err != nil {
		t.Fatalf("Delete err = %v", err)
	}
	if count != 1 {
		t.Fatalf("Delete count = %d, want 1", count)
	}
	if _, err := os.Stat(abs); err == nil {
		t.Fatal("disk file still exists after Delete")
	}
}

func TestDeleteDiskFailureKeepsDBIndex(t *testing.T) {
	s, rows, cfg := newDiskBackedStore(t)

	path := "handoff/task-1/delete.md"
	if _, err := s.Upsert(context.Background(), UpsertParams{
		Path: path, Content: "body", UpdatedBy: "agent",
	}); err != nil {
		t.Fatalf("seed Upsert err = %v", err)
	}
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, path)
	dir := filepath.Dir(abs)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := s.Delete(context.Background(), path)
	if err == nil {
		t.Fatal("Delete err = nil, want disk failure")
	}
	if restoreErr := os.Chmod(dir, 0o755); restoreErr != nil {
		t.Fatalf("restore dir permissions: %v", restoreErr)
	}
	if _, ok := rows.rowOK(path); !ok {
		t.Fatal("DB row missing after failed disk delete")
	}
	disk, readErr := os.ReadFile(abs)
	if readErr != nil {
		t.Fatalf("disk body missing after failed delete: %v", readErr)
	}
	if string(disk) != "body" {
		t.Fatalf("disk body after failed delete = %q, want body", string(disk))
	}
}

func TestList_DoesNotScanDisk(t *testing.T) {
	t.Parallel()
	s, rows, cfg := newDiskBackedStore(t)

	// 只有磁盘文件、没有 DB 行的孤儿文件不应出现在 List 结果里。
	abs := filepath.Join(cfg.CWD, sharedfilefs.SandboxDir, "handoff/orphan/disk-only.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir err = %v", err)
	}
	if err := os.WriteFile(abs, []byte("orphan"), 0o644); err != nil {
		t.Fatalf("seed disk err = %v", err)
	}
	// 两条真实 DB 行才是 List 的索引来源。
	rows.byPath["dag/dag-1/x.md"] = sqlc.SharedFile{Path: "dag/dag-1/x.md"}
	rows.byPath["dag/dag-1/y.md"] = sqlc.SharedFile{Path: "dag/dag-1/y.md"}

	got, err := s.List(context.Background(), ListFilter{Limit: 50})
	if err != nil {
		t.Fatalf("List err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List len = %d, want 2 (disk orphan must be invisible)", len(got))
	}
}

// --- in-memory DB stub ---------------------------------------------------

type fakeRowStore struct {
	mu        sync.Mutex
	byPath    map[string]sqlc.SharedFile
	upsertErr error
	onUpsert  func(sqlc.UpsertSharedFileParams)
}

func newFakeRowStore() *fakeRowStore {
	return &fakeRowStore{byPath: make(map[string]sqlc.SharedFile)}
}

func (f *fakeRowStore) querier() *fakeRowQuerier {
	return &fakeRowQuerier{rows: f}
}

func (f *fakeRowStore) row(path string) sqlc.SharedFile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byPath[path]
}

func (f *fakeRowStore) rowOK(path string) (sqlc.SharedFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.byPath[path]
	return row, ok
}

type fakeRowQuerier struct {
	rows *fakeRowStore
}

func (f *fakeRowQuerier) GetSharedFile(_ context.Context, arg sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
	path := arg.Path
	f.rows.mu.Lock()
	defer f.rows.mu.Unlock()
	row, ok := f.rows.byPath[path]
	if !ok {
		return sqlc.SharedFile{}, platformdb.ErrNotFound
	}
	return row, nil
}

func (f *fakeRowQuerier) ListSharedFiles(_ context.Context, _ sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error) {
	f.rows.mu.Lock()
	defer f.rows.mu.Unlock()
	out := make([]sqlc.SharedFile, 0, len(f.rows.byPath))
	for _, r := range f.rows.byPath {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRowQuerier) DeleteSharedFile(_ context.Context, arg sqlc.DeleteSharedFileParams) (int64, error) {
	path := arg.Path
	f.rows.mu.Lock()
	defer f.rows.mu.Unlock()
	if _, ok := f.rows.byPath[path]; !ok {
		return 0, nil
	}
	delete(f.rows.byPath, path)
	return 1, nil
}

func (f *fakeRowQuerier) UpsertSharedFile(_ context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
	f.rows.mu.Lock()
	if f.rows.upsertErr != nil {
		f.rows.mu.Unlock()
		return sqlc.SharedFile{}, f.rows.upsertErr
	}
	row := sqlc.SharedFile{
		Path:            arg.Path,
		Content:         arg.Content,
		ContentLocation: arg.ContentLocation,
		UpdatedBy:       arg.UpdatedBy,
		CreatedAt:       time.Now().UnixMilli(),
		UpdatedAt:       time.Now().UnixMilli(),
	}
	f.rows.byPath[arg.Path] = row
	onUpsert := f.rows.onUpsert
	f.rows.mu.Unlock()
	if onUpsert != nil {
		onUpsert(arg)
	}
	return row, nil
}
