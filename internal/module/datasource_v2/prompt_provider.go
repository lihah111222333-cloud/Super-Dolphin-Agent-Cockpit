// Package datasourcev2 提供文件正文导入、分块存储和语义检索能力，供 prompt 动态段和前端数据源管理页使用。
package datasourcev2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	datasourceV2PromptChunkLimit    = 10
	maxDatasourceV2PromptBytes      = 32 * 1024
	maxDatasourceV2ChunkPromptBytes = 8 * 1024
)

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
	return contract.DynamicSectionDatasourceV2
}

// Resolve 根据当前 chat 请求做语义检索，并把排序前 10 个 datasource_v2 分块渲染到 prompt。
// 搜索链路失败会作为 critical prompt section error 返回，避免聊天静默丢失数据源上下文。
func (p *PromptProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || p.svc == nil {
		err := errors.New("datasource v2 service is not configured")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasourceV2, err)
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
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasourceV2, err)
	}
	text, err := renderDatasourceV2PromptSection(result.Chunks)
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasourceV2, err)
	}
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

// renderDatasourceV2PromptSection 将检索结果渲染为 prompt 段，并按总量与单块字节预算裁剪。
func renderDatasourceV2PromptSection(chunks []SemanticChunkResult) (string, error) {
	if len(chunks) == 0 {
		return "", nil
	}
	lines := make([]string, 0, len(chunks)*4+3)
	lines = append(lines,
		"## "+contract.DynamicSectionDatasourceV2,
		"",
		"Uploaded datasource_v2 semantic matches for the current chat request. Chunks are ordered by semantic similarity.",
	)
	rank := 0
	usedBytes := 0
	omittedChunks := 0
	clippedChunks := 0
	for _, chunk := range chunks {
		content := strings.TrimSpace(chunk.Content)
		if content == "" {
			omittedChunks++
			continue
		}
		available := maxDatasourceV2PromptBytes - usedBytes
		if available <= 0 {
			omittedChunks++
			continue
		}
		limit := min(maxDatasourceV2ChunkPromptBytes, available)
		truncatedContent, clipped := truncateDatasourceV2PromptContent(content, limit)
		truncatedContent = strings.TrimSpace(truncatedContent)
		if truncatedContent == "" {
			omittedChunks++
			continue
		}
		if clipped {
			clippedChunks++
		}
		rank++
		title := datasourceV2PromptChunkTitle(rank, chunk)
		lines = append(lines,
			"",
			title,
			truncatedContent,
		)
		usedBytes += len(title) + len(truncatedContent) + 2
	}
	if rank == 0 {
		return "", errors.New("datasource_v2 prompt budget removed every chunk")
	}
	if omittedChunks > 0 || clippedChunks > 0 {
		lines = append(lines,
			"",
			fmt.Sprintf("Datasource v2 prompt truncated: %d chunks omitted; %d chunks clipped; total budget %d bytes; per-chunk budget %d bytes.",
				omittedChunks, clippedChunks, maxDatasourceV2PromptBytes, maxDatasourceV2ChunkPromptBytes),
		)
	}
	return strings.Join(lines, "\n"), nil
}

// truncateDatasourceV2PromptContent 按字节上限截断文本，并避免切断 UTF-8 rune。
func truncateDatasourceV2PromptContent(content string, limit int) (string, bool) {
	if limit <= 0 {
		return "", strings.TrimSpace(content) != ""
	}
	if len(content) <= limit {
		return content, false
	}
	if limit > len(content) {
		limit = len(content)
	}
	for limit > 0 && !utf8.ValidString(content[:limit]) {
		_, size := utf8.DecodeLastRuneInString(content[:limit])
		if size <= 0 {
			limit--
			continue
		}
		limit -= size
	}
	return content[:limit], true
}

func datasourceV2PromptChunkTitle(rank int, chunk SemanticChunkResult) string {
	name := strings.TrimSpace(chunk.FileName)
	if name == "" {
		name = fmt.Sprintf("document %d", chunk.DocumentID)
	}
	return fmt.Sprintf("### %d. %s [chunk %d]", rank, name, chunk.ChunkIndex)
}
