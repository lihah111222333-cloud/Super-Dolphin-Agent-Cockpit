package orchestration

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/nodeexec"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/sharedfilemeta"
	sharedfilestore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sharedfile"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
)

// sharedfile_adapter.go 把 sharedfile store 适配成 nodeexec.SharedFileReader /
// SharedFileWriter，供 NodeExecutor RunContext 读取和写入共享文件。
//
// 设计要点：
//   - Reader 端口是三态 (content, exists, err)：store.Get 返
//     platformdb.ErrNotFound 翻成 exists=false（不是 err），让 nodeexec.inputs.go
//     的 validation classify 走得通；其他 err 仍透出，让 dispatcher 走 transient retry。
//   - Writer 端口接 store.Upsert：路径白名单由 store 内 sharedfilepath.ValidateWritePath
//     拦截；adapter 不重复 enforce；UpdatedBy 写 "node-router"（生产 RunContext 暂未带
//     调用方身份，使用稳定 marker 方便审计）。
//   - nil store 输入 → 返 nil 适配器，让 RunContext 字段保持 nil（向后兼容）；
//     若节点 cfg 里又引用 sharedfile，由 nodeexec.inputs.go 归 validation 错。

// storeSharedFileReaderAdapter 把 sharedfilestore.Reader 适配成 nodeexec.SharedFileReader。
type storeSharedFileReaderAdapter struct {
	store sharedfilestore.Reader
}

// NewStoreSharedFileReader 暴露 reader 适配器给 fx 层。nil store → nil adapter。
func NewStoreSharedFileReader(store sharedfilestore.Reader) nodeexec.SharedFileReader {
	if store == nil {
		return nil
	}
	return &storeSharedFileReaderAdapter{store: store}
}

// ReadSharedFile 实现 nodeexec.SharedFileReader：
//   - 存在 → (content, true, nil)
//   - 不存在（platformdb.ErrNotFound）→ ("", false, nil)
//   - 其他错（路径校验失败 / DB 抽风 / IO 错）→ ("", false, err)
func (a *storeSharedFileReaderAdapter) ReadSharedFile(ctx context.Context, path string) (string, bool, error) {
	if a == nil || a.store == nil {
		return "", false, errors.New("store sharedfile reader: nil receiver")
	}
	file, err := a.store.Get(ctx, path)
	if err != nil {
		if errors.Is(err, platformdb.ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("store sharedfile reader: get %q: %w", path, err)
	}
	if file == nil {
		return "", false, nil
	}
	return file.Content, true, nil
}

// storeSharedFileWriterAdapter 把 sharedfilestore.Store 适配成 nodeexec.SharedFileWriter。
type storeSharedFileWriterAdapter struct {
	store sharedfilestore.Store
	*sharedfilemeta.StoreWriter
}

// sharedFileWriterUpdatedBy 是 dispatcher 路径写入 sharedfile 时填的 updated_by 标识。
// 生产 RunContext 暂未带节点级身份；固定值让审计和日志检索能定位这类写入。
const sharedFileWriterUpdatedBy = "node-router"

// NewStoreSharedFileWriter 暴露 writer 适配器给 fx 层。nil store → nil adapter。
func NewStoreSharedFileWriter(store sharedfilestore.Store) nodeexec.SharedFileWriter {
	if store == nil {
		return nil
	}
	return &storeSharedFileWriterAdapter{store: store, StoreWriter: sharedfilemeta.NewStoreWriter(store)}
}

// WriteSharedFile 实现 nodeexec.SharedFileWriter：把 (path, content) Upsert 进 sharedfile store。
// 路径白名单 + 磁盘原子写 + DB 写都由 store.Upsert 一站处理；adapter 只收窄签名。
func (a *storeSharedFileWriterAdapter) WriteSharedFile(ctx context.Context, path, content string) error {
	if a == nil || a.store == nil {
		return errors.New("store sharedfile writer: nil receiver")
	}
	if _, err := a.store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      path,
		Content:   content,
		UpdatedBy: sharedFileWriterUpdatedBy,
	}); err != nil {
		return fmt.Errorf("store sharedfile writer: upsert %q: %w", path, err)
	}
	return nil
}
