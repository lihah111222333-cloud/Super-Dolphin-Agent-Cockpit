package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfileport"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

type memorySharedFileStoreTestDouble struct {
	get    func(context.Context, string) (*sharedfilestore.SharedFile, error)
	list   func(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error)
	delete func(context.Context, string) (int64, error)
}

func (s *memorySharedFileStoreTestDouble) Get(
	ctx context.Context,
	path string,
) (*sharedfilestore.SharedFile, error) {
	if s.get != nil {
		return s.get(ctx, path)
	}
	return nil, nil
}

func (s *memorySharedFileStoreTestDouble) List(
	ctx context.Context,
	filter sharedfilestore.ListFilter,
) ([]sharedfilestore.SharedFile, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return nil, nil
}

func (s *memorySharedFileStoreTestDouble) Delete(ctx context.Context, path string) (int64, error) {
	if s.delete != nil {
		return s.delete(ctx, path)
	}
	return 0, nil
}

var _ sharedfilestore.Reader = (*memorySharedFileStoreTestDouble)(nil)
var _ sharedfilestore.Deleter = (*memorySharedFileStoreTestDouble)(nil)

// TestMemorySharedFileProvidersPreserveOptionalNil 固定直接 nil 与 typed nil Store 都投影为 nil domain port。
func TestMemorySharedFileProvidersPreserveOptionalNil(t *testing.T) {
	if provideMemorySharedFileReader(nil) != nil {
		t.Fatal("expected nil memory shared-file reader")
	}
	if provideMemorySharedFileDeleter(nil) != nil {
		t.Fatal("expected nil memory shared-file deleter")
	}
	var typedNil *memorySharedFileStoreTestDouble
	if got := provideMemorySharedFileReader(typedNil); got != nil {
		t.Fatalf("expected typed nil Store to produce nil reader, got %T", got)
	}
	if got := provideMemorySharedFileDeleter(typedNil); got != nil {
		t.Fatalf("expected typed nil Store to produce nil deleter, got %T", got)
	}
}

// TestMemorySharedFileAdapterFieldCoverage 用 one-hot 输入覆盖 filter 与文件 DTO 的全部字段。
func TestMemorySharedFileAdapterFieldCoverage(t *testing.T) {
	t.Run("list_filter", func(t *testing.T) {
		assertBusinessStoreAdapterFieldsMap(t, func(filter sharedfileport.ListFilter) (sharedfilestore.ListFilter, error) {
			return toStoreMemorySharedFileListFilter(filter), nil
		})
	})
	t.Run("shared_file", func(t *testing.T) {
		assertBusinessStoreAdapterFieldsMap(t, func(file sharedfilestore.SharedFile) (sharedfileport.File, error) {
			return fromStoreMemorySharedFile(file), nil
		})
	})
}

// TestMemorySharedFileReaderAdapterMapsGetAndList 固定 App adapter 的真实参数与结果映射。
func TestMemorySharedFileReaderAdapterMapsGetAndList(t *testing.T) {
	createdAt := time.Date(2026, time.July, 10, 1, 2, 3, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	wantStored := sharedfilestore.SharedFile{
		Path:      "memory/team.md",
		Content:   "body",
		UpdatedBy: "memory-test",
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	store := &memorySharedFileStoreTestDouble{
		get: func(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
			if path != wantStored.Path {
				t.Fatalf("Get path = %q, want %q", path, wantStored.Path)
			}
			file := wantStored
			return &file, nil
		},
		list: func(_ context.Context, filter sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
			wantFilter := sharedfilestore.ListFilter{Prefix: "memory/", Limit: 41}
			if filter != wantFilter {
				t.Fatalf("List filter = %#v, want %#v", filter, wantFilter)
			}
			return []sharedfilestore.SharedFile{wantStored}, nil
		},
	}
	reader := provideMemorySharedFileReader(store)
	got, err := reader.Get(context.Background(), wantStored.Path)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := fromStoreMemorySharedFile(wantStored)
	if got == nil || *got != want {
		t.Fatalf("Get = %#v, want %#v", got, want)
	}
	listed, err := reader.List(context.Background(), sharedfileport.ListFilter{Prefix: "memory/", Limit: 41})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0] != want {
		t.Fatalf("List = %#v, want [%#v]", listed, want)
	}
}

// TestMemorySharedFileReaderAdapterRejectsNilFile 固定 Store 成功但返回 nil file 时显式失败。
func TestMemorySharedFileReaderAdapterRejectsNilFile(t *testing.T) {
	store := &memorySharedFileStoreTestDouble{
		get: func(context.Context, string) (*sharedfilestore.SharedFile, error) { return nil, nil },
	}
	got, err := provideMemorySharedFileReader(store).Get(context.Background(), "memory/missing.md")
	if got != nil || !errors.Is(err, errMemorySharedFileStoreReturnedNil) {
		t.Fatalf("Get = (%#v, %v), want nil file error", got, err)
	}
}

// TestMemorySharedFileAdaptersPreserveStoreErrors 固定 Get/List/Delete 普通错误对象不被替换或吞掉。
func TestMemorySharedFileAdaptersPreserveStoreErrors(t *testing.T) {
	wantErr := errors.New("shared file store failed")
	store := &memorySharedFileStoreTestDouble{
		get: func(context.Context, string) (*sharedfilestore.SharedFile, error) {
			return nil, wantErr
		},
		list: func(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
			return nil, wantErr
		},
		delete: func(context.Context, string) (int64, error) {
			return 0, wantErr
		},
	}
	reader := provideMemorySharedFileReader(store)
	deleter := provideMemorySharedFileDeleter(store)
	tests := map[string]func() error{
		"get": func() error {
			_, err := reader.Get(context.Background(), "memory/failure.md")
			return err
		},
		"list": func() error {
			_, err := reader.List(context.Background(), sharedfileport.ListFilter{})
			return err
		},
		"delete": func() error {
			_, err := deleter.Delete(context.Background(), "memory/failure.md")
			return err
		},
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			gotErr := run()
			if gotErr != wantErr || !errors.Is(gotErr, wantErr) {
				t.Fatalf("error = %v, want identical %v", gotErr, wantErr)
			}
		})
	}
}

// TestMemorySharedFileListReturnsIndependentSlice 固定 App 返回的领域切片不与 Store 结果共享 backing array。
func TestMemorySharedFileListReturnsIndependentSlice(t *testing.T) {
	stored := []sharedfilestore.SharedFile{{Path: "memory/original.md"}}
	store := &memorySharedFileStoreTestDouble{
		list: func(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
			return stored, nil
		},
	}
	got, err := provideMemorySharedFileReader(store).List(context.Background(), sharedfileport.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got[0].Path = "memory/mutated.md"
	if stored[0].Path != "memory/original.md" {
		t.Fatalf("Store slice mutated through domain result: %#v", stored)
	}
}

// TestMemorySharedFileDeleterAdapterPreservesCount 固定删除行数经 App adapter 原样返回。
func TestMemorySharedFileDeleterAdapterPreservesCount(t *testing.T) {
	store := &memorySharedFileStoreTestDouble{
		delete: func(_ context.Context, path string) (int64, error) {
			if path != "memory/delete.md" {
				t.Fatalf("Delete path = %q", path)
			}
			return 41, nil
		},
	}
	count, err := provideMemorySharedFileDeleter(store).Delete(context.Background(), "memory/delete.md")
	if err != nil || count != 41 {
		t.Fatalf("Delete = (%d, %v), want (41, nil)", count, err)
	}
}
