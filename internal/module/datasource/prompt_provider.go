// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.DynamicSectionProvider = (*PromptProvider)(nil)

// PromptProvider 把 datasource 工作区的文件列表或文档正文接入 prompt 动态段。
type PromptProvider struct {
	svc Service
}

// NewPromptProvider 创建promptprovider。
func NewPromptProvider(svc Service) *PromptProvider {
	return &PromptProvider{svc: svc}
}

// SectionName 处理section名称。
func (p *PromptProvider) SectionName() string {
	return contract.DynamicSectionDatasource
}

// Resolve 把当前工作区的数据源内容整理进动态提示段。
func (p *PromptProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || p.svc == nil {
		err := errors.New("datasource service is not configured")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	documents, err := p.svc.ListDocuments(ctx, contract.SectionContextCWD(input))
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	if text := renderDatasourceDocumentPromptSection(documents.Documents); text != "" {
		return &text, nil
	}
	result, err := p.svc.ListFiles(ctx)
	if err != nil {
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
	}
	text := renderDatasourcePromptSection(result.FileNames)
	if text == "" {
		return nil, nil
	}
	return &text, nil
}

// renderDatasourcePromptSection 将文件名列表渲染为 prompt 段落文本。
func renderDatasourcePromptSection(fileNames []string) string {
	names := make([]string, 0, len(fileNames))
	for _, name := range fileNames {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	lines := make([]string, 0, len(names)+3)
	lines = append(lines,
		"## "+contract.DynamicSectionDatasource,
		"",
		"Uploaded datasource files available in this workspace. Do not infer file contents from names alone.",
	)
	for _, name := range names {
		lines = append(lines, "- "+name)
	}
	return strings.Join(lines, "\n")
}

// renderDatasourceDocumentPromptSection 将文档内容列表渲染为带分节标题的 prompt 段落。
func renderDatasourceDocumentPromptSection(documents []DatasourceDocument) string {
	documents = normalizeDatasourceDocuments(documents)
	if len(documents) == 0 {
		return ""
	}
	lines := make([]string, 0, len(documents)*5+3)
	lines = append(lines,
		"## "+contract.DynamicSectionDatasource,
		"",
		"Uploaded datasource file contents available in this workspace.",
	)
	for _, document := range documents {
		lines = append(lines,
			"",
			"### "+document.Name,
			strings.TrimSpace(document.Content),
		)
	}
	return strings.Join(lines, "\n")
}

// normalizeDatasourceDocuments 过滤空名称或空内容的文档并按名称升序排序。
func normalizeDatasourceDocuments(documents []DatasourceDocument) []DatasourceDocument {
	normalized := make([]DatasourceDocument, 0, len(documents))
	for _, document := range documents {
		document.Name = strings.TrimSpace(document.Name)
		document.Content = strings.TrimSpace(document.Content)
		if document.Name != "" && document.Content != "" {
			normalized = append(normalized, document)
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		return normalized[i].Name < normalized[j].Name
	})
	return normalized
}
