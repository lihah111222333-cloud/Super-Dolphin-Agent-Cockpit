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
		{name: "manual.PDF", content: []byte("%PDF-1.7\nsource"), ext: ".pdf"},
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

func TestUploadFileRejectsUnsupportedExtension(t *testing.T) {
	svc := NewService()
	project := t.TempDir()
	t.Chdir(project)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "notes.md")
	if err := os.WriteFile(source, []byte("markdown"), 0o600); err != nil {
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
