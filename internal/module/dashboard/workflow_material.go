package dashboard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sharedfilepath "github.com/anthropic-ai/super-agent-v3/internal/platform/sharedfilepath"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
)

const workflowMaterialUploadPrefix = "reports/workflows/uploads/"

// WriteWorkflowMaterial 将前端模板上传的材料写入 workflow 专用 sharedfile 前缀。
func (s *service) WriteWorkflowMaterial(ctx context.Context, req WorkflowMaterialWriteRequest) (*sharedfilestore.SharedFile, error) {
	writer, ok := s.sharedFiles.(sharedfilestore.Upserter)
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
	return writer.Upsert(ctx, sharedfilestore.UpsertParams{
		Path:      cleanedPath,
		Content:   req.Content,
		UpdatedBy: dashboardUICreatedBy,
	})
}
