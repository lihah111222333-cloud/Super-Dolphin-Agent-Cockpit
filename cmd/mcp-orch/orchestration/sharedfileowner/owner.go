package sharedfileowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
)

// ErrInvalidOwner 表示 DAG 输出 sharedfile 缺失必要所有权字段。
var ErrInvalidOwner = errors.New("invalid sharedfile owner")

// Owner 标识一个 sharedfile 输出属于哪个 DAG run、节点、thread 和 turn。
type Owner struct {
	DagKey   string
	NodeKey  string
	RunID    int64
	ThreadID string
	TurnID   string
}

// marker 是写入 _internal/dag-output-ownership 的磁盘元数据格式。
type marker struct {
	DagKey    string `json:"dag_key"`
	NodeKey   string `json:"node_key"`
	RunID     int64  `json:"run_id"`
	ThreadID  string `json:"thread_id"`
	TurnID    string `json:"turn_id"`
	UpdatedAt string `json:"updated_at"`
}

// HasCurrent 同时检查正文和 owner marker，确认当前文件仍属于指定 DAG 节点输出。
func HasCurrent(ctx context.Context, reader nodeexec.SharedFileReader, path string, owner Owner) (bool, error) {
	if reader == nil {
		return false, errors.New("SharedFileReader not wired")
	}
	if err := validate(owner); err != nil {
		return false, err
	}
	if _, exists, err := reader.ReadSharedFile(ctx, path); err != nil || !exists {
		return false, err
	}
	raw, exists, err := reader.ReadSharedFile(ctx, MarkerPath(path))
	if err != nil || !exists {
		return false, err
	}
	return matches(raw, owner), nil
}

// Write 先写正文再写 owner marker；marker 写失败时向上返回，调用方可重试整次输出。
func Write(ctx context.Context, writer nodeexec.SharedFileWriter, path, content string, owner Owner) error {
	if writer == nil {
		return errors.New("SharedFileWriter not wired")
	}
	if err := validate(owner); err != nil {
		return err
	}
	if err := writer.WriteSharedFile(ctx, path, content); err != nil {
		return fmt.Errorf("write %q: %w", path, err)
	}
	markerPath := MarkerPath(path)
	if err := writer.WriteSharedFile(ctx, markerPath, encode(owner)); err != nil {
		return fmt.Errorf("write owner marker %q: %w", markerPath, err)
	}
	return nil
}

// MarkerPath 返回正文路径对应的内部 owner marker 路径。
func MarkerPath(path string) string {
	return "_internal/dag-output-ownership/" + strings.TrimSpace(path) + ".metadata.json"
}

// IsValidation 判断错误是否为 owner 字段校验失败。
func IsValidation(err error) bool { return errors.Is(err, ErrInvalidOwner) }

// validate 校验 owner 必填字段，缺失时返回 ErrInvalidOwner 包装错误。
func validate(owner Owner) error {
	switch {
	case strings.TrimSpace(owner.DagKey) == "":
		return fmt.Errorf("%w: missing dag_key", ErrInvalidOwner)
	case strings.TrimSpace(owner.NodeKey) == "":
		return fmt.Errorf("%w: missing node_key", ErrInvalidOwner)
	case owner.RunID <= 0:
		return fmt.Errorf("%w: missing run_id", ErrInvalidOwner)
	case strings.TrimSpace(owner.ThreadID) == "":
		return fmt.Errorf("%w: missing thread_id", ErrInvalidOwner)
	case strings.TrimSpace(owner.TurnID) == "":
		return fmt.Errorf("%w: missing turn_id", ErrInvalidOwner)
	}
	return nil
}

// encode 将 owner 序列化为 marker JSON，并写入当前 UTC 更新时间。
func encode(owner Owner) string {
	raw, _ := json.Marshal(marker{
		DagKey:    strings.TrimSpace(owner.DagKey),
		NodeKey:   strings.TrimSpace(owner.NodeKey),
		RunID:     owner.RunID,
		ThreadID:  strings.TrimSpace(owner.ThreadID),
		TurnID:    strings.TrimSpace(owner.TurnID),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
	return string(raw)
}

// matches 判断 marker 是否完整、时间格式有效，且与当前 owner 完全一致。
func matches(raw string, owner Owner) bool {
	var got marker
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &got); err != nil {
		return false
	}
	if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(got.UpdatedAt)); err != nil {
		return false
	}
	return got.DagKey == strings.TrimSpace(owner.DagKey) && got.NodeKey == strings.TrimSpace(owner.NodeKey) &&
		got.RunID == owner.RunID && got.ThreadID == strings.TrimSpace(owner.ThreadID) && got.TurnID == strings.TrimSpace(owner.TurnID)
}
