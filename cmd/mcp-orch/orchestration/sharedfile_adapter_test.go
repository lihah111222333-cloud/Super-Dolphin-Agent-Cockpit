package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

type stubSharedfileStore struct {
	file    *sharedfilestore.SharedFile
	err     error
	upserts []sharedfilestore.UpsertParams
}

func (s stubSharedfileStore) Get(context.Context, string) (*sharedfilestore.SharedFile, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.file, nil
}

func (s stubSharedfileStore) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *stubSharedfileStore) Upsert(_ context.Context, params sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	s.upserts = append(s.upserts, params)
	return &sharedfilestore.SharedFile{Path: params.Path, Content: params.Content, UpdatedBy: params.UpdatedBy}, nil
}

func (s *stubSharedfileStore) Delete(context.Context, string) (int64, error) {
	return 0, nil
}

func TestStoreSharedFileReaderNotFoundIsExplicitMissing(t *testing.T) {
	t.Parallel()

	reader := NewStoreSharedFileReader(stubSharedfileStore{err: platformdb.ErrNotFound})
	content, exists, err := reader.ReadSharedFile(context.Background(), "missing.md")
	if err != nil {
		t.Fatalf("ReadSharedFile() error = %v, want nil for explicit missing state", err)
	}
	if exists || content != "" {
		t.Fatalf("ReadSharedFile() = (%q,%v), want empty,false", content, exists)
	}
}

func TestStoreSharedFileReaderSurfacesStoreErrors(t *testing.T) {
	t.Parallel()

	reader := NewStoreSharedFileReader(stubSharedfileStore{err: errors.New("db offline")})
	_, _, err := reader.ReadSharedFile(context.Background(), "plan.md")
	if err == nil || !strings.Contains(err.Error(), "db offline") {
		t.Fatalf("ReadSharedFile() error = %v, want wrapped store error", err)
	}
}
