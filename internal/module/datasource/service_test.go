package datasource

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestUploadFileCopiesPDFAndTXTToWorkingDirUploadDir(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()

	tests := []struct {
		name    string
		content []byte
		ext     string
	}{
		{name: "notes.txt", content: []byte("plain text source"), ext: ".txt"},
		{name: "manual.PDF", content: minimalPDFWithText("pdf source"), ext: ".pdf"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := filepath.Join(sourceDir, tt.name)
			if err := os.WriteFile(source, tt.content, 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}

			got, err := svc.UploadFile(context.Background(), UploadFileRequest{
				SourcePath: source,
			})
			if err != nil {
				t.Fatalf("UploadFile() error = %v", err)
			}

			wantPath := filepath.Join(project, ".agent", "datasources", "uploads", tt.name)
			if got.StoredPath != wantPath {
				t.Fatalf("StoredPath = %q, want %q", got.StoredPath, wantPath)
			}
			if got.Name != tt.name {
				t.Fatalf("Name = %q, want %q", got.Name, tt.name)
			}
			if got.Extension != tt.ext {
				t.Fatalf("Extension = %q, want %q", got.Extension, tt.ext)
			}
			if got.Size != int64(len(tt.content)) {
				t.Fatalf("Size = %d, want %d", got.Size, len(tt.content))
			}

			written, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("read stored file: %v", err)
			}
			if !bytes.Equal(written, tt.content) {
				t.Fatalf("stored content = %q, want %q", written, tt.content)
			}
		})
	}
}

func TestUploadFileOverwritesExistingTargetWithSameName(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "测试.txt")
	if err := os.WriteFile(source, []byte("new datasource content"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	targetPath := filepath.Join(uploadDir, "测试.txt")
	if err := os.WriteFile(targetPath, []byte("stale datasource content"), 0o600); err != nil {
		t.Fatalf("write existing target: %v", err)
	}

	got, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}
	if got.StoredPath != targetPath {
		t.Fatalf("StoredPath = %q, want %q", got.StoredPath, targetPath)
	}
	written, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read stored file: %v", err)
	}
	if !bytes.Equal(written, []byte("new datasource content")) {
		t.Fatalf("stored content = %q, want overwritten content", written)
	}
}

func TestUploadFilePersistsExtractedTextContent(t *testing.T) {
	store := &recordingDatasourceStore{}
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "notes.txt")
	if err := os.WriteFile(source, []byte("plain text source"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}

	if store.upsertCalls != 1 {
		t.Fatalf("store upsert calls = %d, want 1", store.upsertCalls)
	}
	if store.upserted.WorkspaceRoot != project {
		t.Fatalf("WorkspaceRoot = %q, want %q", store.upserted.WorkspaceRoot, project)
	}
	if store.upserted.Name != "notes.txt" || store.upserted.Extension != ".txt" {
		t.Fatalf("upserted identity = %+v", store.upserted)
	}
	if store.upserted.Content != "plain text source" {
		t.Fatalf("Content = %q, want extracted text", store.upserted.Content)
	}
	if store.upserted.StoredPath != got.StoredPath {
		t.Fatalf("StoredPath = %q, want %q", store.upserted.StoredPath, got.StoredPath)
	}
}

func TestUploadFilePersistsDecodedUTF16TextContent(t *testing.T) {
	store := &recordingDatasourceStore{}
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "notes.txt")
	if err := os.WriteFile(source, utf16LEWithBOM("hello text datasource"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}

	if store.upserted.Content != "hello text datasource" {
		t.Fatalf("Content = %q, want decoded UTF-16 text", store.upserted.Content)
	}
}

func TestUploadFileRejectsUnsupportedTextEncoding(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "bad.txt")
	if err := os.WriteFile(source, []byte{0xff, 0xfe, 0xfd}, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if !errors.Is(err, errUnsupportedTextEncoding) {
		t.Fatalf("UploadFile() error = %v, want errUnsupportedTextEncoding", err)
	}
	if _, statErr := os.Stat(filepath.Join(project, ".agent", "datasources", "uploads")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("upload dir stat error = %v, want not exist", statErr)
	}
}

func TestUploadFilePersistsExtractedPDFTextContent(t *testing.T) {
	store := &recordingDatasourceStore{}
	svc := NewServiceWithStore(store)
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "manual.pdf")
	if err := os.WriteFile(source, minimalPDFWithText("Hello PDF datasource"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if err != nil {
		t.Fatalf("UploadFile() error = %v", err)
	}

	if store.upserted.Content != "Hello PDF datasource" {
		t.Fatalf("Content = %q, want extracted PDF text", store.upserted.Content)
	}
}

func TestUploadFileRejectsUnsupportedExtension(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "image.png")
	if err := os.WriteFile(source, []byte{0x89, 'P', 'N', 'G'}, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := svc.UploadFile(context.Background(), UploadFileRequest{
		SourcePath: source,
	})
	if !errors.Is(err, errUnsupportedFileExtension) {
		t.Fatalf("UploadFile() error = %v, want errUnsupportedFileExtension", err)
	}
	if _, statErr := os.Stat(filepath.Join(project, ".agent", "datasources", "uploads")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("upload dir stat error = %v, want not exist", statErr)
	}
}

func TestUploadFileRequiresSourcePath(t *testing.T) {
	svc := NewService()
	_, err := svc.UploadFile(context.Background(), UploadFileRequest{})
	if err == nil || !strings.Contains(err.Error(), "sourcePath is required") {
		t.Fatalf("missing source path error = %v", err)
	}
}

func TestListFilesReturnsSortedFileNamesFromUploadDir(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	for _, name := range []string{"zeta.txt", "alpha.pdf"} {
		if err := os.WriteFile(filepath.Join(uploadDir, name), []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(uploadDir, "nested"), 0o755); err != nil {
		t.Fatalf("create nested dir: %v", err)
	}

	got, err := svc.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	want := []string{"alpha.pdf", "zeta.txt"}
	if !slices.Equal(got.FileNames, want) {
		t.Fatalf("FileNames = %#v, want %#v", got.FileNames, want)
	}
}

func TestListFilesReturnsEmptyWhenUploadDirDoesNotExist(t *testing.T) {
	svc := NewService()
	t.Chdir(t.TempDir())

	got, err := svc.ListFiles(context.Background())
	if err != nil {
		t.Fatalf("ListFiles() error = %v", err)
	}
	if len(got.FileNames) != 0 {
		t.Fatalf("FileNames = %#v, want empty", got.FileNames)
	}
}

func TestDeleteFileRemovesOnlyNamedDatasourceFile(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	uploadDir := filepath.Join(project, ".agent", "datasources", "uploads")
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		t.Fatalf("create upload dir: %v", err)
	}
	removedPath := filepath.Join(uploadDir, "remove.txt")
	keptPath := filepath.Join(uploadDir, "keep.pdf")
	if err := os.WriteFile(removedPath, []byte("remove me"), 0o600); err != nil {
		t.Fatalf("write removed file: %v", err)
	}
	if err := os.WriteFile(keptPath, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write kept file: %v", err)
	}

	got, err := svc.DeleteFile(context.Background(), DeleteFileRequest{FileName: "remove.txt"})
	if err != nil {
		t.Fatalf("DeleteFile() error = %v", err)
	}
	if !got.Deleted {
		t.Fatalf("Deleted = false, want true")
	}
	if got.Name != "remove.txt" {
		t.Fatalf("Name = %q, want remove.txt", got.Name)
	}
	if _, err := os.Stat(removedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("removed file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("kept file stat error = %v", err)
	}
}

func TestDeleteFileRejectsUnsafeFileName(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	outsidePath := filepath.Join(project, ".agent", "datasources", "outside.txt")
	if err := os.MkdirAll(filepath.Dir(outsidePath), 0o755); err != nil {
		t.Fatalf("create outside dir: %v", err)
	}
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	tests := []string{"", "../outside.txt", "nested/file.txt", "nested\\file.txt", "."}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := svc.DeleteFile(context.Background(), DeleteFileRequest{FileName: name})
			if !errors.Is(err, errInvalidDatasourceFileName) {
				t.Fatalf("DeleteFile() error = %v, want errInvalidDatasourceFileName", err)
			}
			if _, statErr := os.Stat(outsidePath); statErr != nil {
				t.Fatalf("outside file stat error = %v", statErr)
			}
		})
	}
}

func TestDeleteFileReturnsNotFoundForMissingDatasourceFile(t *testing.T) {
	svc := NewService()
	t.Chdir(t.TempDir())

	_, err := svc.DeleteFile(context.Background(), DeleteFileRequest{FileName: "missing.txt"})
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("DeleteFile() error = %v, want os.ErrNotExist", err)
	}
}

type recordingDatasourceStore struct {
	upsertCalls int
	upserted    UpsertDatasourceDocumentParams
	documents   []DatasourceDocument
}

func (s *recordingDatasourceStore) UpsertDocument(_ context.Context, params UpsertDatasourceDocumentParams) error {
	s.upsertCalls++
	s.upserted = params
	return nil
}

func (s *recordingDatasourceStore) ListDocuments(context.Context, string) ([]DatasourceDocument, error) {
	return s.documents, nil
}

func (s *recordingDatasourceStore) DeleteDocument(context.Context, string, string) error {
	return nil
}

func minimalPDFWithText(text string) []byte {
	body := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET"
	return []byte("%PDF-1.4\n" +
		"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >> endobj\n" +
		"4 0 obj << /Length 0 >> stream\n" + body + "\nendstream endobj\n" +
		"5 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n" +
		"trailer << /Root 1 0 R >>\n%%EOF\n")
}

func utf16LEWithBOM(text string) []byte {
	codeUnits := utf16.Encode([]rune(text))
	out := []byte{0xFF, 0xFE}
	for _, codeUnit := range codeUnits {
		out = append(out, byte(codeUnit), byte(codeUnit>>8))
	}
	return out
}
