package sharedfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
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
	byPath    map[string]sqlc.SharedFile
	upsertErr error
}

func newFakeRowStore() *fakeRowStore {
	return &fakeRowStore{byPath: make(map[string]sqlc.SharedFile)}
}

func (f *fakeRowStore) querier() *fakeRowQuerier {
	return &fakeRowQuerier{rows: f}
}

type fakeRowQuerier struct {
	rows *fakeRowStore
}

func (f *fakeRowQuerier) GetSharedFile(_ context.Context, arg sqlc.GetSharedFileParams) (sqlc.SharedFile, error) {
	path := arg.Path
	row, ok := f.rows.byPath[path]
	if !ok {
		return sqlc.SharedFile{}, platformdb.ErrNotFound
	}
	return row, nil
}

func (f *fakeRowQuerier) ListSharedFiles(_ context.Context, _ sqlc.ListSharedFilesParams) ([]sqlc.SharedFile, error) {
	out := make([]sqlc.SharedFile, 0, len(f.rows.byPath))
	for _, r := range f.rows.byPath {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRowQuerier) DeleteSharedFile(_ context.Context, arg sqlc.DeleteSharedFileParams) (int64, error) {
	path := arg.Path
	if _, ok := f.rows.byPath[path]; !ok {
		return 0, nil
	}
	delete(f.rows.byPath, path)
	return 1, nil
}

func (f *fakeRowQuerier) UpsertSharedFile(_ context.Context, arg sqlc.UpsertSharedFileParams) (sqlc.SharedFile, error) {
	if f.rows.upsertErr != nil {
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
	return row, nil
}
