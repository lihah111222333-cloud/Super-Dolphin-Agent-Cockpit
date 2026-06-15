package sharedfile

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	sharedfilefs "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilefs"
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
		SourcePath: sourcePath,
		TargetPath: targetRel,
		Overwrite:  "fail",
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
