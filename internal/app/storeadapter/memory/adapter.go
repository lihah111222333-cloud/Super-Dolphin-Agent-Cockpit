package memoryadapter

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/sharedfileport"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

var errMemorySharedFileStoreReturnedNil = errors.New("shared file store returned nil file")

type memorySharedFileReaderAdapter struct {
	reader sharedfilestore.Reader
}

type memorySharedFileDeleterAdapter struct {
	deleter sharedfilestore.Deleter
}

var _ sharedfileport.Reader = (*memorySharedFileReaderAdapter)(nil)
var _ sharedfileport.Deleter = (*memorySharedFileDeleterAdapter)(nil)

// provideMemorySharedFileReader 把 Store reader 投影为 memory-owned 窄端口。
func provideMemorySharedFileReader(reader sharedfilestore.Reader) sharedfileport.Reader {
	if storeguard.IsNil(reader) {
		return nil
	}
	return &memorySharedFileReaderAdapter{reader: reader}
}

// provideMemorySharedFileDeleter 把 Store deleter 投影为 memory-owned 窄端口。
func provideMemorySharedFileDeleter(deleter sharedfilestore.Deleter) sharedfileport.Deleter {
	if storeguard.IsNil(deleter) {
		return nil
	}
	return &memorySharedFileDeleterAdapter{deleter: deleter}
}

// Get 读取单个 shared file，并在 App 边界完成 DTO 转换。
func (a *memorySharedFileReaderAdapter) Get(ctx context.Context, path string) (*sharedfileport.File, error) {
	file, err := a.reader.Get(ctx, path)
	if err != nil {
		return nil, err
	}
	if file == nil {
		return nil, errMemorySharedFileStoreReturnedNil
	}
	converted := fromStoreMemorySharedFile(*file)
	return &converted, nil
}

// List 列出 shared file，并返回与 Store 结果切片独立的领域 DTO。
func (a *memorySharedFileReaderAdapter) List(
	ctx context.Context,
	filter sharedfileport.ListFilter,
) ([]sharedfileport.File, error) {
	files, err := a.reader.List(ctx, toStoreMemorySharedFileListFilter(filter))
	if err != nil {
		return nil, err
	}
	out := make([]sharedfileport.File, len(files))
	for index, file := range files {
		out[index] = fromStoreMemorySharedFile(file)
	}
	return out, nil
}

// Delete 删除指定 shared file，保留 Store 的行数与错误语义。
func (a *memorySharedFileDeleterAdapter) Delete(ctx context.Context, path string) (int64, error) {
	return a.deleter.Delete(ctx, path)
}

func toStoreMemorySharedFileListFilter(filter sharedfileport.ListFilter) sharedfilestore.ListFilter {
	return sharedfilestore.ListFilter{
		Prefix: filter.Prefix,
		Limit:  filter.Limit,
	}
}

func fromStoreMemorySharedFile(file sharedfilestore.SharedFile) sharedfileport.File {
	return sharedfileport.File{
		Path:      file.Path,
		Content:   file.Content,
		UpdatedBy: file.UpdatedBy,
		CreatedAt: file.CreatedAt,
		UpdatedAt: file.UpdatedAt,
	}
}
