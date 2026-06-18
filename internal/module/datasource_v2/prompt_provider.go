package datasourcev2

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2store "github.com/anthropic-ai/super-agent-v3/internal/store/datasourcev2"
)

const datasourceV2PromptDocumentLimit = 100

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

// Resolve 读取 datasource_v2 的 ready 文档和分块正文，渲染到聊天 system prompt。
// 读取失败会作为 critical prompt section error 返回，避免聊天静默丢失数据源上下文。
func (p *PromptProvider) Resolve(ctx context.Context, _ contract.SectionContext) (*string, error) {
	if p == nil || p.svc == nil {
		err := errors.New("datasource v2 service is not configured")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	documents, err := p.svc.ListDocuments(ctx, ListDocumentsRequest{
		Keyword: datasourcev2store.StatusReady,
		Limit:   datasourceV2PromptDocumentLimit,
	})
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	items, err := p.resolvePromptDocuments(ctx, documents.Documents)
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	text := renderDatasourceV2PromptSection(items)
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

type datasourceV2PromptDocument struct {
	name    string
	content string
}

func (p *PromptProvider) resolvePromptDocuments(
	ctx context.Context,
	documents []DocumentResult,
) ([]datasourceV2PromptDocument, error) {
	items := make([]datasourceV2PromptDocument, 0, len(documents))
	for _, document := range documents {
		if document.Status != datasourcev2store.StatusReady || document.DocumentID <= 0 {
			continue
		}
		detail, err := p.svc.GetDocument(ctx, GetDocumentRequest{DocumentID: document.DocumentID})
		if err != nil {
			return nil, err
		}
		if item, ok := datasourceV2PromptDocumentFromDetail(detail); ok {
			items = append(items, item)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].name < items[j].name
	})
	return items, nil
}

func datasourceV2PromptDocumentFromDetail(detail GetDocumentResult) (datasourceV2PromptDocument, bool) {
	if detail.Document.Status != datasourcev2store.StatusReady {
		return datasourceV2PromptDocument{}, false
	}
	name := strings.TrimSpace(detail.Document.FileName)
	content := datasourceV2PromptChunksText(detail.Chunks)
	if name == "" || content == "" {
		return datasourceV2PromptDocument{}, false
	}
	return datasourceV2PromptDocument{name: name, content: content}, true
}

func datasourceV2PromptChunksText(chunks []TextChunkResult) string {
	var builder strings.Builder
	for _, chunk := range chunks {
		builder.WriteString(chunk.Content)
	}
	return strings.TrimSpace(builder.String())
}

func renderDatasourceV2PromptSection(documents []datasourceV2PromptDocument) string {
	if len(documents) == 0 {
		return ""
	}
	lines := make([]string, 0, len(documents)*4+3)
	lines = append(lines,
		"## "+contract.DynamicSectionDatasource,
		"",
		"Uploaded datasource_v2 file contents available in this workspace.",
	)
	for _, document := range documents {
		lines = append(lines,
			"",
			"### "+document.name,
			document.content,
		)
	}
	return strings.Join(lines, "\n")
}
