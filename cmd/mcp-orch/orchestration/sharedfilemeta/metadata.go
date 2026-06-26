package sharedfilemeta

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/nodeexec"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sharedfile"
	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// sharedFileMetadataMaxBytes 限制单次带元数据写入正文大小，避免内部 marker 路径承载大文件。
const sharedFileMetadataMaxBytes = 1024 * 1024

// sharedFileMetadataRecord 是正文 sharedfile 对应的 owner/producer 元数据格式。
type sharedFileMetadataRecord struct {
	Path          string `json:"path"`
	OwnerNode     string `json:"owner_node"`
	ProducerActor string `json:"producer_actor"`
	ContentType   string `json:"content_type"`
	RunID         int64  `json:"run_id,omitempty"`
	PromptRef     string `json:"prompt_ref,omitempty"`
	UpdatedAt     string `json:"updated_at"`
}

// StoreWriter 将 sharedfile store 扩展为带 owner/producer 元数据的 writer。
type StoreWriter struct {
	store sharedfilestore.Store
}

// NewStoreWriter 创建带元数据标记能力的 sharedfile writer。
func NewStoreWriter(store sharedfilestore.Store) *StoreWriter {
	if store == nil {
		return nil
	}
	return &StoreWriter{store: store}
}

// WriteSharedFileWithMetadata 写入 sharedfile 正文和 owner/producer 元数据标记。
func (w *StoreWriter) WriteSharedFileWithMetadata(ctx context.Context, req nodeexec.SharedFileWriteRequest) error {
	if w == nil || w.store == nil {
		return errors.New("store sharedfile writer: nil receiver")
	}
	cleaned, err := ValidateWriteRequest(req)
	if err != nil {
		return err
	}
	actor := strings.TrimSpace(req.ProducerActor)
	if _, err := w.store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      cleaned,
		Content:   req.Content,
		UpdatedBy: actor,
	}); err != nil {
		return fmt.Errorf("store sharedfile writer: upsert %q: %w", cleaned, err)
	}
	markerContent, err := MarshalMetadata(cleaned, req)
	if err != nil {
		return err
	}
	markerPath := MetadataPath(cleaned)
	if _, err := w.store.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      markerPath,
		Content:   markerContent,
		UpdatedBy: actor,
	}); err != nil {
		return fmt.Errorf("store sharedfile writer: upsert metadata %q: %w", markerPath, err)
	}
	return nil
}

// ValidateWriteRequest 校验带审计元数据的 sharedfile 写入请求，并返回清理后的路径。
func ValidateWriteRequest(req nodeexec.SharedFileWriteRequest) (string, error) {
	cleaned, err := sharedfilepath.ValidateWritePath(req.Path)
	if err != nil {
		return "", err
	}
	switch {
	case strings.TrimSpace(req.OwnerNode) == "":
		return "", errors.New("sharedfile write owner_node is required")
	case strings.TrimSpace(req.ProducerActor) == "":
		return "", errors.New("sharedfile write producer_actor is required")
	case !isAllowedSharedFileContentType(req.ContentType):
		return "", fmt.Errorf("sharedfile write content_type %q is not allowed", req.ContentType)
	case len([]byte(req.Content)) > sharedFileMetadataMaxBytes:
		return "", fmt.Errorf("sharedfile write content exceeds %d bytes", sharedFileMetadataMaxBytes)
	}
	if hasControlCharacter(req.OwnerNode) || hasControlCharacter(req.ProducerActor) || hasControlCharacter(req.PromptRef) {
		return "", errors.New("sharedfile write metadata contains control characters")
	}
	return cleaned, nil
}

// isAllowedSharedFileContentType 判断 sharedfile metadata 允许记录的内容类型。
func isAllowedSharedFileContentType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "application/json", "application/octet-stream", "text/markdown", "text/plain":
		return true
	default:
		return false
	}
}

// hasControlCharacter 拒绝元数据字段里的换行和 NUL，避免 marker JSON 被日志/展示截断。
func hasControlCharacter(value string) bool {
	return strings.ContainsAny(value, "\x00\r\n")
}

// MarshalMetadata 序列化 sharedfile owner/producer 标记，供内部元数据文件写入。
func MarshalMetadata(path string, req nodeexec.SharedFileWriteRequest) (string, error) {
	raw, err := json.Marshal(sharedFileMetadataRecord{
		Path:          path,
		OwnerNode:     strings.TrimSpace(req.OwnerNode),
		ProducerActor: strings.TrimSpace(req.ProducerActor),
		ContentType:   strings.ToLower(strings.TrimSpace(req.ContentType)),
		RunID:         req.RunID,
		PromptRef:     strings.TrimSpace(req.PromptRef),
		UpdatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", fmt.Errorf("marshal sharedfile metadata: %w", err)
	}
	return string(raw), nil
}

// MetadataPath 返回正文 sharedfile 对应的内部元数据标记路径。
func MetadataPath(path string) string {
	return "_internal/sharedfile_meta/" + strings.ReplaceAll(path, "/", "__") + ".json"
}
