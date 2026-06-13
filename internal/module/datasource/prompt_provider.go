package datasource

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var _ contract.DynamicSectionProvider = (*PromptProvider)(nil)

type PromptProvider struct {
	svc Service
}

func NewPromptProvider(svc Service) *PromptProvider {
	return &PromptProvider{svc: svc}
}

func (p *PromptProvider) SectionName() string {
	return contract.DynamicSectionDatasource
}

func (p *PromptProvider) Resolve(ctx context.Context, _ contract.SectionContext) (*string, error) {
	if p == nil || p.svc == nil {
		err := errors.New("datasource service is not configured")
		return nil, contract.NewCriticalPromptSectionError(contract.DynamicSectionDatasource, err)
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
