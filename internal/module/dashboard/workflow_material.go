package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
)

// workflowMaterialUploadPrefix 限定 dashboard 可写入的 workflow 材料目录。
const workflowMaterialUploadPrefix = "reports/workflows/uploads/"

// WriteWorkflowMaterial 将前端模板上传材料写入 workflow 专用 sharedfile 前缀。
// sharedFiles 必须支持 Upserter；路径和内容都校验通过后才写入，避免 dashboard 任意写 sharedfile。
func (s *service) WriteWorkflowMaterial(ctx context.Context, req WorkflowMaterialWriteRequest) (*SharedFile, error) {
	writer, ok := s.sharedFiles.(SharedFileWriter)
	if !ok || writer == nil {
		return nil, errors.New("dashboard: writable sharedfile store is not configured")
	}
	cleanedPath, err := sharedfilepath.ValidateWritePath(req.Path)
	if err != nil {
		return nil, fmt.Errorf("dashboard: invalid workflow material path: %w", err)
	}
	if !strings.HasPrefix(cleanedPath, workflowMaterialUploadPrefix) {
		return nil, fmt.Errorf("dashboard: workflow material path must be under %s", workflowMaterialUploadPrefix)
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, errors.New("dashboard: workflow material content is required")
	}
	return writer.Upsert(ctx, SharedFileUpsertParams{
		Path:      cleanedPath,
		Content:   req.Content,
		UpdatedBy: dashboardUICreatedBy,
	})
}
