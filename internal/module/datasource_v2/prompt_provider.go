package datasourcev2

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const datasourceV2PromptChunkLimit = 10

var _ contract.DynamicSectionProvider = (*PromptProvider)(nil)

// PromptProvider 把 datasource_v2 表里的 ready 文档正文接入 prompt 动态段。
type PromptProvider struct {
	svc Service
}

// NewPromptProvider 创建 datasource_v2 的 prompt provider。
func NewPromptProvider(svc Service) *PromptProvider {
	return &PromptProvider{svc: svc}
}

// SectionName 返回 datasource_v2 写入的 prompt section 名称。
func (p *PromptProvider) SectionName() string {
	return contract.DynamicSectionDatasource
}

// Resolve 根据当前 chat 请求做语义检索，并把排序前 10 个 datasource_v2 分块渲染到 prompt。
// 搜索链路失败会作为 critical prompt section error 返回，避免聊天静默丢失数据源上下文。
func (p *PromptProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || p.svc == nil {
		err := errors.New("datasource v2 service is not configured")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	query := datasourceV2PromptQuery(input)
	if query == "" {
		return nil, nil
	}
	result, err := p.svc.SearchRelevantChunks(ctx, SearchRelevantChunksRequest{
		Query: query,
		Limit: datasourceV2PromptChunkLimit,
	})
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	text := renderDatasourceV2PromptSection(result.Chunks)
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

func datasourceV2PromptQuery(input contract.SectionContext) string {
	if input.Turn != nil {
		return strings.TrimSpace(input.Turn.UserText)
	}
	if input.Start != nil {
		return strings.TrimSpace(input.Start.Prompt)
	}
	return ""
}

func renderDatasourceV2PromptSection(chunks []SemanticChunkResult) string {
	if len(chunks) == 0 {
		return ""
	}
	lines := make([]string, 0, len(chunks)*4+3)
	lines = append(lines,
		"## "+contract.DynamicSectionDatasource,
		"",
		"Uploaded datasource_v2 semantic matches for the current chat request. Chunks are ordered by semantic similarity.",
	)
	rank := 0
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk.Content)
		if content == "" {
			continue
		}
		rank++
		lines = append(lines,
			"",
			datasourceV2PromptChunkTitle(rank, chunk),
			content,
		)
	}
	if rank == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func datasourceV2PromptChunkTitle(rank int, chunk SemanticChunkResult) string {
	name := strings.TrimSpace(chunk.FileName)
	if name == "" {
		name = strings.TrimSpace(chunk.SourcePath)
	}
	if name == "" {
		name = fmt.Sprintf("document %d", chunk.DocumentID)
	}
	return fmt.Sprintf("### %d. %s [chunk %d]", rank, name, chunk.ChunkIndex)
}
