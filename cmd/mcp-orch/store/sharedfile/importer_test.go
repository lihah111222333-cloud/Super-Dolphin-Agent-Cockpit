package sharedfile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	sharedfilefs "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilefs"
	sharedfilegitignore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/sharedfilegitignore"
	_ "modernc.org/sqlite"
)

func TestImportLocalFile_CopiesBinaryToSharedfileAndKeepsDBContentEmpty(t *testing.T) {
	t.Parallel()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "final.mp4")
	wantBytes := []byte{0, 1, 2, 3, 4, 0xff}
	if err := os.WriteFile(sourcePath, wantBytes, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	db := newFakeImportDB(t)
	sfStore := newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1})

	got, err := sfStore.ImportLocalFile(context.Background(), ImportLocalFileParams{
		SourcePath:         sourcePath,
		TargetPath:         "dag/douyin/daily-video/run-1/final.mp4",
		AllowedExtensions:  []string{".mp4"},
		AllowedSourceRoots: []string{sourceRoot},
		MaxBytes:           int64(len(wantBytes)),
		Overwrite:          "fail",
		UpdatedBy:          "dag-artifact",
		ContentType:        "video/mp4",
	})
	if err != nil {
		t.Fatalf("ImportLocalFile() error = %v", err)
	}
	if got.Path != "dag/douyin/daily-video/run-1/final.mp4" {
		t.Fatalf("Path = %q", got.Path)
	}
	targetPath := filepath.Join(sfStore.cfg.SandboxRoot(), got.Path)
	gotBytes, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(gotBytes) != string(wantBytes) {
		t.Fatalf("target bytes = %v, want %v", gotBytes, wantBytes)
	}
	assertSharedFileMetadata(t, db, got.Path, "", "dag-artifact")
}

func TestImportLocalFile_RejectsUnsafeInputs(t *testing.T) {
	t.Parallel()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "final.mp4")
	if err := os.WriteFile(sourcePath, []byte("12345"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sourceDir := filepath.Join(sourceRoot, "dir.mp4")
	if err := os.Mkdir(sourceDir, 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	symlinkPath := filepath.Join(sourceRoot, "link.mp4")
	symlinkSupported := true
	if err := os.Symlink(sourcePath, symlinkPath); err != nil {
		symlinkSupported = false
	}
	for _, tc := range unsafeImportCases(sourcePath, sourceDir, symlinkPath, t.TempDir()) {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "source_symlink" && !symlinkSupported {
				t.Skip("symlink unsupported by current Windows privilege")
			}
			sfStore := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1})
			_, err := sfStore.ImportLocalFile(context.Background(), tc.params)
			if err == nil {
				t.Fatalf("ImportLocalFile() error = nil, want rejection")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestImportLocalFile_RejectsAllowedRootSymlinkedParentEscape(t *testing.T) {
	t.Parallel()
	allowedRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsideParent := filepath.Join(outsideRoot, "outside-parent")
	if err := os.Mkdir(outsideParent, 0o755); err != nil {
		t.Fatalf("mkdir outside parent: %v", err)
	}
	outsideSource := filepath.Join(outsideParent, "final.mp4")
	if err := os.WriteFile(outsideSource, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside source: %v", err)
	}
	symlinkedParent := filepath.Join(allowedRoot, "linked-parent")
	if err := os.Symlink(outsideParent, symlinkedParent); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	sfStore := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1})
	_, err := sfStore.ImportLocalFile(context.Background(), ImportLocalFileParams{
		SourcePath:         filepath.Join(symlinkedParent, "final.mp4"),
		TargetPath:         "dag/run-1/final.mp4",
		AllowedExtensions:  []string{".mp4"},
		AllowedSourceRoots: []string{allowedRoot},
		Overwrite:          "fail",
	})
	if err == nil {
		t.Fatalf("ImportLocalFile() error = nil, want allowed_source_roots rejection")
	}
	if !errors.Is(err, ErrImportValidation) || !strings.Contains(err.Error(), "allowed_source_roots") {
		t.Fatalf("ImportLocalFile() error = %v, want allowed_source_roots validation rejection", err)
	}
}

func TestImportLocalFile_FailsBeforeCopyWhenGitignoreEnsureFails(t *testing.T) {
	t.Parallel()

	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "final.mp4")
	if err := os.WriteFile(sourcePath, []byte("video"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".gitignore"), 0o755); err != nil {
		t.Fatalf("mkdir .gitignore sentinel: %v", err)
	}
	sharedfilegitignore.ResetForTests()
	t.Cleanup(sharedfilegitignore.ResetForTests)
	db := newFakeImportDB(t)
	sfStore := newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{CWD: cwd, InlineThresholdBytes: 1})

	_, err := sfStore.ImportLocalFile(context.Background(), ImportLocalFileParams{
		SourcePath:         sourcePath,
		TargetPath:         "dag/run-1/final.mp4",
		AllowedExtensions:  []string{".mp4"},
		AllowedSourceRoots: []string{sourceRoot},
		Overwrite:          "fail",
	})
	if err == nil || !strings.Contains(err.Error(), "sharedfilegitignore") {
		t.Fatalf("ImportLocalFile() error = %v, want gitignore ensure failure", err)
	}
	targetAbs := filepath.Join(cwd, ".agnet", "shared", "dag", "run-1", "final.mp4")
	if _, statErr := os.Stat(targetAbs); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target stat error = %v, want not exist", statErr)
	}
	var count int
	if scanErr := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM shared_files WHERE path = ?`, "dag/run-1/final.mp4").Scan(&count); scanErr != nil {
		t.Fatalf("query shared_files: %v", scanErr)
	}
	if count != 0 {
		t.Fatalf("shared_files rows = %d, want 0 after gitignore failure", count)
	}
}

type unsafeImportCase struct {
	name   string
	params ImportLocalFileParams
	want   string
}

func unsafeImportCases(sourcePath, sourceDir, symlinkPath, outsideRoot string) []unsafeImportCase {
	return []unsafeImportCase{
		{name: "target_traversal", params: ImportLocalFileParams{SourcePath: sourcePath, TargetPath: "../final.mp4"}, want: "path traversal"},
		{name: "source_directory", params: ImportLocalFileParams{SourcePath: sourceDir, TargetPath: "dag/run-1/final.mp4"}, want: "directory"},
		{name: "source_symlink", params: ImportLocalFileParams{SourcePath: symlinkPath, TargetPath: "dag/run-1/final.mp4"}, want: "symlink"},
		{name: "over_max_bytes", params: ImportLocalFileParams{SourcePath: sourcePath, TargetPath: "dag/run-1/final.mp4", MaxBytes: 4}, want: "max_bytes"},
		{name: "missing_allowed_source_roots", params: ImportLocalFileParams{SourcePath: sourcePath, TargetPath: "dag/run-1/final.mp4"}, want: "allowed_source_roots"},
		{name: "outside_allowed_source_roots", params: ImportLocalFileParams{SourcePath: sourcePath, TargetPath: "dag/run-1/final.mp4", AllowedSourceRoots: []string{outsideRoot}}, want: "allowed_source_roots"},
		{name: "extension_rejected", params: ImportLocalFileParams{SourcePath: sourcePath, TargetPath: "dag/run-1/final.mp4", AllowedExtensions: []string{".mov"}}, want: "allowed_extensions"},
	}
}

func TestImportLocalFile_OverwriteFailRejectsExistingTarget(t *testing.T) {
	t.Parallel()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "final.mp4")
	if err := os.WriteFile(sourcePath, []byte("new"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sfStore := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1})
	targetRel := "dag/run-1/final.mp4"
	targetAbs, err := sfStore.cfg.ResolveAbs(targetRel)
	if err != nil {
		t.Fatalf("ResolveAbs: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte("old"), 0o644); err != nil {
		t.Fatalf("write existing target: %v", err)
	}

	_, err = sfStore.ImportLocalFile(context.Background(), ImportLocalFileParams{
		SourcePath:         sourcePath,
		TargetPath:         targetRel,
		AllowedSourceRoots: []string{sourceRoot},
		Overwrite:          "fail",
	})
	if err == nil || !strings.Contains(err.Error(), "overwrite") {
		t.Fatalf("ImportLocalFile() error = %v, want overwrite rejection", err)
	}
	got, err := os.ReadFile(targetAbs)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("target content = %q, want original old content preserved", got)
	}
}

func TestImportLocalFile_RejectsAllowedRootParentSymlinkEscape(t *testing.T) {
	t.Parallel()
	allowedRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsideDir := filepath.Join(outsideRoot, "real")
	if err := os.Mkdir(outsideDir, 0o755); err != nil {
		t.Fatalf("mkdir outside dir: %v", err)
	}
	outsideFile := filepath.Join(outsideDir, "final.mp4")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	linkDir := filepath.Join(allowedRoot, "linked")
	if err := os.Symlink(outsideDir, linkDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	sfStore := newStoreWithConfig(sqlc.New(newFakeImportDB(t)), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1})
	_, err := sfStore.ImportLocalFile(context.Background(), ImportLocalFileParams{
		SourcePath:         filepath.Join(linkDir, "final.mp4"),
		TargetPath:         "dag/run-1/final.mp4",
		AllowedExtensions:  []string{".mp4"},
		AllowedSourceRoots: []string{allowedRoot},
	})
	if err == nil {
		t.Fatal("ImportLocalFile() error = nil, want parent-symlink root escape rejection")
	}
	if !strings.Contains(err.Error(), "allowed_source_roots") {
		t.Fatalf("error = %v, want allowed_source_roots rejection", err)
	}
}

func TestImportLocalFile_DurableFailureMatrixRestoresEmptyState(t *testing.T) {
	t.Parallel()
	faultErr := errors.New("injected filesystem failure")
	for _, tc := range []struct {
		name             string
		configure        func(importFileOps, string) importFileOps
		wantRollbackFail bool
	}{
		{name: "temp_fsync", configure: func(ops importFileOps, _ string) importFileOps { return failTempSync(ops, faultErr) }},
		{name: "temp_close", configure: func(ops importFileOps, _ string) importFileOps { return failTempClose(ops, faultErr) }},
		{name: "rename", configure: func(ops importFileOps, _ string) importFileOps { return failRename(ops, faultErr) }},
		{name: "directory_fsync", configure: func(ops importFileOps, _ string) importFileOps { return failDirectorySync(ops, faultErr) }},
		{name: "directory_close", configure: func(ops importFileOps, _ string) importFileOps { return failDirectoryClose(ops, faultErr) }},
		{
			name: "rollback_remove",
			configure: func(ops importFileOps, target string) importFileOps {
				return failRemove(failDirectorySync(ops, faultErr), target, errors.New("rollback remove failed"))
			},
			wantRollbackFail: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sfStore, db, sourceRoot, sourcePath := newDurableImportFixture(t)
			targetRel := "dag/run-1/final.mp4"
			targetAbs, err := sfStore.cfg.ResolveWriteAbs(targetRel)
			if err != nil {
				t.Fatalf("ResolveWriteAbs() error = %v", err)
			}
			params := durableImportParams(sourceRoot, sourcePath, targetRel)
			_, err = sfStore.importLocalFileToTargetWithOps(context.Background(), params, targetRel, targetAbs, tc.configure(defaultImportFileOps(), targetAbs))
			if !errors.Is(err, faultErr) {
				t.Fatalf("import error = %v, want injected failure", err)
			}
			assertImportIndexCount(t, db, targetRel, 0)
			assertDurableImportFailureState(t, targetAbs, err, tc.wantRollbackFail)
		})
	}
}

func assertDurableImportFailureState(t *testing.T, targetAbs string, importErr error, wantRollbackFail bool) {
	t.Helper()
	_, statErr := os.Stat(targetAbs)
	if wantRollbackFail {
		if importErr == nil || !strings.Contains(importErr.Error(), "rollback remove failed") {
			t.Fatalf("import error = %v, want rollback remove failure", importErr)
		}
		if statErr != nil {
			t.Fatalf("target stat error = %v, want retained file after rollback failure", statErr)
		}
		return
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target stat error = %v, want no published file", statErr)
	}
	leftovers, globErr := filepath.Glob(targetAbs + ".tmp-*")
	if globErr != nil || len(leftovers) != 0 {
		t.Fatalf("temporary leftovers = %v, error = %v", leftovers, globErr)
	}
}

func TestImportLocalFile_SQLiteUpsertAndRollbackDeleteFailuresAreJoined(t *testing.T) {
	t.Parallel()
	sfStore, db, sourceRoot, sourcePath := newDurableImportFixture(t)
	targetRel := "dag/run-1/final.mp4"
	if _, err := sqlc.New(db).UpsertSharedFile(context.Background(), sqlc.UpsertSharedFileParams{Path: targetRel, Content: "", ContentLocation: contentLocationDisk, UpdatedBy: "old-writer"}); err != nil {
		t.Fatalf("seed previous index: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_import_upsert BEFORE UPDATE ON shared_files WHEN NEW.updated_by = 'new-writer' BEGIN SELECT RAISE(FAIL, 'upsert failed'); END;`); err != nil {
		t.Fatalf("create upsert trigger: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_import_rollback BEFORE UPDATE ON shared_files WHEN NEW.updated_by = 'old-writer' BEGIN SELECT RAISE(FAIL, 'rollback restore failed'); END;`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}
	_, err := sfStore.ImportLocalFile(context.Background(), durableImportParams(sourceRoot, sourcePath, targetRel))
	if err == nil || !strings.Contains(err.Error(), "upsert failed") || !strings.Contains(err.Error(), "rollback restore failed") {
		t.Fatalf("ImportLocalFile() error = %v, want joined upsert and rollback failures", err)
	}
}

func TestImportLocalFile_RestoresPreviousFileAndIndexAfterDirectoryFailure(t *testing.T) {
	t.Parallel()
	sfStore, db, sourceRoot, sourcePath := newDurableImportFixture(t)
	targetRel := "dag/run-1/final.mp4"
	targetAbs, err := sfStore.cfg.ResolveWriteAbs(targetRel)
	if err != nil {
		t.Fatalf("ResolveWriteAbs() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.WriteFile(targetAbs, []byte("old-body"), 0o640); err != nil {
		t.Fatalf("write old target: %v", err)
	}
	q := sqlc.New(db)
	if _, err := q.UpsertSharedFile(context.Background(), sqlc.UpsertSharedFileParams{Path: targetRel, Content: "", ContentLocation: contentLocationDisk, UpdatedBy: "old-writer"}); err != nil {
		t.Fatalf("seed shared file: %v", err)
	}
	faultErr := errors.New("directory fsync failed")
	params := durableImportParams(sourceRoot, sourcePath, targetRel)
	_, err = sfStore.importLocalFileToTargetWithOps(context.Background(), params, targetRel, targetAbs, failDirectorySync(defaultImportFileOps(), faultErr))
	if !errors.Is(err, faultErr) {
		t.Fatalf("import error = %v, want directory failure", err)
	}
	body, readErr := os.ReadFile(targetAbs)
	if readErr != nil || string(body) != "old-body" {
		t.Fatalf("restored body = %q, error = %v", body, readErr)
	}
	assertSharedFileMetadata(t, db, targetRel, "", "old-writer")
}

func TestDetectImportDriftFindsTempsMissingAndUnindexedFiles(t *testing.T) {
	t.Parallel()
	sfStore, db, _, _ := newDurableImportFixture(t)
	root := sfStore.cfg.SandboxRoot()
	missingRel := "dag/missing.mp4"
	if _, err := sqlc.New(db).UpsertSharedFile(context.Background(), sqlc.UpsertSharedFileParams{Path: missingRel, Content: "", ContentLocation: contentLocationDisk, UpdatedBy: "seed"}); err != nil {
		t.Fatalf("seed missing index: %v", err)
	}
	for rel, body := range map[string]string{
		"dag/orphan.mp4":               "orphan",
		"dag/final.mp4.tmp-incomplete": "temp",
	} {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	drift, err := sfStore.detectImportDrift(context.Background())
	if err != nil {
		t.Fatalf("detectImportDrift() error = %v", err)
	}
	if fmt.Sprint(drift.TempPaths) != "[dag/final.mp4.tmp-incomplete]" || fmt.Sprint(drift.MissingIndexedPaths) != "[dag/missing.mp4]" || fmt.Sprint(drift.UnindexedPaths) != "[dag/orphan.mp4]" {
		t.Fatalf("drift = %#v", drift)
	}
}

func newDurableImportFixture(t *testing.T) (*store, *fakeImportDB, string, string) {
	t.Helper()
	sourceRoot := t.TempDir()
	sourcePath := filepath.Join(sourceRoot, "final.mp4")
	if err := os.WriteFile(sourcePath, []byte("new-body"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	db := newFakeImportDB(t)
	return newStoreWithConfig(sqlc.New(db), sharedfilefs.Config{CWD: t.TempDir(), InlineThresholdBytes: 1}), db, sourceRoot, sourcePath
}

func durableImportParams(sourceRoot, sourcePath, targetRel string) ImportLocalFileParams {
	return ImportLocalFileParams{
		SourcePath: sourcePath, TargetPath: targetRel, AllowedExtensions: []string{".mp4"},
		AllowedSourceRoots: []string{sourceRoot}, MaxBytes: 1024, Overwrite: "replace", UpdatedBy: "new-writer",
	}
}

type faultingImportFile struct {
	importTempFile
	syncErr  error
	closeErr error
}

func (f faultingImportFile) Sync() error {
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.importTempFile.Sync()
}

func (f faultingImportFile) Close() error {
	closeErr := f.importTempFile.Close()
	return errors.Join(closeErr, f.closeErr)
}

type faultingImportDir struct {
	importSyncCloser
	syncErr  error
	closeErr error
}

func (d faultingImportDir) Sync() error {
	if d.syncErr != nil {
		return d.syncErr
	}
	return d.importSyncCloser.Sync()
}

func (d faultingImportDir) Close() error {
	closeErr := d.importSyncCloser.Close()
	return errors.Join(closeErr, d.closeErr)
}

func failTempSync(ops importFileOps, fault error) importFileOps {
	createTemp := ops.createTemp
	ops.createTemp = func(dir, pattern string) (importTempFile, error) {
		file, err := createTemp(dir, pattern)
		return faultingImportFile{importTempFile: file, syncErr: fault}, err
	}
	return ops
}

func failTempClose(ops importFileOps, fault error) importFileOps {
	createTemp := ops.createTemp
	ops.createTemp = func(dir, pattern string) (importTempFile, error) {
		file, err := createTemp(dir, pattern)
		return faultingImportFile{importTempFile: file, closeErr: fault}, err
	}
	return ops
}

func failRename(ops importFileOps, fault error) importFileOps {
	ops.rename = func(string, string) error { return fault }
	return ops
}

func failDirectorySync(ops importFileOps, fault error) importFileOps {
	openDir := ops.openDir
	ops.openDir = func(path string) (importSyncCloser, error) {
		dir, err := openDir(path)
		return faultingImportDir{importSyncCloser: dir, syncErr: fault}, err
	}
	return ops
}

func failDirectoryClose(ops importFileOps, fault error) importFileOps {
	openDir := ops.openDir
	ops.openDir = func(path string) (importSyncCloser, error) {
		dir, err := openDir(path)
		return faultingImportDir{importSyncCloser: dir, closeErr: fault}, err
	}
	return ops
}

func failRemove(ops importFileOps, target string, fault error) importFileOps {
	remove := ops.remove
	ops.remove = func(path string) error {
		if filepath.Clean(path) == filepath.Clean(target) {
			return fault
		}
		return remove(path)
	}
	return ops
}

func assertImportIndexCount(t *testing.T, db *fakeImportDB, path string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shared_files WHERE path = ?`, path).Scan(&count); err != nil {
		t.Fatalf("query shared_files: %v", err)
	}
	if count != want {
		t.Fatalf("shared_files count = %d, want %d", count, want)
	}
}

var _ io.Writer = faultingImportFile{}

type fakeImportDB struct {
	*sql.DB
}

func newFakeImportDB(t *testing.T) *fakeImportDB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(context.Background(), `
CREATE TABLE shared_files (
  path TEXT PRIMARY KEY,
  content TEXT NOT NULL,
  content_location TEXT NOT NULL DEFAULT 'inline' CHECK (content_location IN ('inline', 'disk')),
  updated_by TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`); err != nil {
		t.Fatalf("create shared_files table: %v", err)
	}
	return &fakeImportDB{DB: db}
}

func assertSharedFileMetadata(t *testing.T, db *fakeImportDB, path, wantContent, wantUpdatedBy string) {
	t.Helper()
	var gotContent, gotUpdatedBy string
	if err := db.QueryRowContext(context.Background(), `SELECT content, updated_by FROM shared_files WHERE path = ?`, path).Scan(&gotContent, &gotUpdatedBy); err != nil {
		t.Fatalf("query shared file metadata: %v", err)
	}
	if gotContent != wantContent {
		t.Fatalf("DB content = %q, want %q", gotContent, wantContent)
	}
	if gotUpdatedBy != wantUpdatedBy {
		t.Fatalf("UpdatedBy = %q, want %q", gotUpdatedBy, wantUpdatedBy)
	}
}
