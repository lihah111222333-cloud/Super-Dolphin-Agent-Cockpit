package workflowtemplates

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed assets/manifest.json assets/government-enterprise/*.yaml
var embeddedAssets embed.FS

// Registry 负责加载和渲染内置工作流模板，保持只读且无数据库副作用。
type Registry struct {
	order     []string
	templates map[string]Template
}

type manifest struct {
	Version   int      `json:"version"`
	Templates []string `json:"templates"`
}

// NewDefaultRegistry 从仓库内置资源加载政企工作流模板。
func NewDefaultRegistry() (*Registry, error) {
	return Load(embeddedAssets)
}

// Load 从文件系统加载 manifest 和模板资源。
func Load(fsys fs.FS) (*Registry, error) {
	raw, err := fs.ReadFile(fsys, "assets/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("workflowtemplates: read manifest.json: %w", err)
	}
	var mf manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, fmt.Errorf("workflowtemplates: parse manifest.json: %w", err)
	}
	if mf.Version <= 0 {
		return nil, errors.New("workflowtemplates: manifest.version is required")
	}
	if len(mf.Templates) == 0 {
		return nil, errors.New("workflowtemplates: manifest has no templates")
	}
	return loadTemplates(fsys, mf.Templates)
}

// ListTemplates 按 manifest 顺序返回模板摘要，可选按业务流、输出类型和定时能力筛选。
func (r *Registry) ListTemplates(filters ...ListFilter) []TemplateSummary {
	if r == nil {
		return nil
	}
	filter := firstListFilter(filters)
	out := make([]TemplateSummary, 0, len(r.order))
	for _, id := range r.order {
		summary := templateSummary(r.templates[id])
		if templateSummaryMatches(summary, filter) {
			out = append(out, summary)
		}
	}
	return out
}

// GetTemplate 返回指定模板的完整定义。
func (r *Registry) GetTemplate(id string) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	tpl, ok := r.templates[strings.TrimSpace(id)]
	return tpl, ok
}

// RenderDAGDraft 根据用户参数渲染 DAG 草案，不落库、不启动 DAG、不写文件。
func (r *Registry) RenderDAGDraft(req RenderRequest) (DAGDraft, error) {
	tpl, ok := r.GetTemplate(req.TemplateID)
	if !ok {
		return DAGDraft{}, fmt.Errorf("workflowtemplates: template %q not found", req.TemplateID)
	}
	if err := checkTemplateVersion(tpl, req.Version); err != nil {
		return DAGDraft{}, err
	}
	values := normalizedValues(renderValues(req))
	if err := requireFields(tpl, values); err != nil {
		return DAGDraft{}, err
	}
	nodes := renderNodes(tpl.DAGTemplate.Nodes, values)
	finalOutput := tpl.FinalOutput
	finalOutput.PathTemplate = renderString(finalOutput.PathTemplate, values)
	if err := validateRenderedOutputPaths(tpl, nodes, finalOutput); err != nil {
		return DAGDraft{}, err
	}
	return buildDAGDraft(tpl, req, values, nodes, finalOutput), nil
}

func loadTemplates(fsys fs.FS, items []string) (*Registry, error) {
	reg := &Registry{
		order:     make([]string, 0, len(items)),
		templates: make(map[string]Template, len(items)),
	}
	for _, item := range items {
		tpl, err := loadTemplate(fsys, item)
		if err != nil {
			return nil, err
		}
		if err := validateTemplate(tpl); err != nil {
			return nil, fmt.Errorf("workflowtemplates: validate %s: %w", item, err)
		}
		if _, exists := reg.templates[tpl.ID]; exists {
			return nil, fmt.Errorf("workflowtemplates: duplicate template id %q", tpl.ID)
		}
		reg.order = append(reg.order, tpl.ID)
		reg.templates[tpl.ID] = tpl
	}
	return reg, nil
}

func loadTemplate(fsys fs.FS, item string) (Template, error) {
	clean := filepath.ToSlash(filepath.Clean(item))
	if clean == "." || strings.HasPrefix(clean, "../") || filepath.IsAbs(clean) {
		return Template{}, fmt.Errorf("workflowtemplates: invalid manifest path %q", item)
	}
	raw, err := fs.ReadFile(fsys, "assets/"+clean)
	if err != nil {
		return Template{}, fmt.Errorf("workflowtemplates: read template %s: %w", item, err)
	}
	var tpl Template
	if err := yaml.Unmarshal(raw, &tpl); err != nil {
		return Template{}, fmt.Errorf("workflowtemplates: parse template %s: %w", item, err)
	}
	return tpl, nil
}

func renderNodes(nodes []NodeTemplate, values map[string]string) []NodeTemplate {
	out := make([]NodeTemplate, 0, len(nodes))
	for _, node := range nodes {
		rendered := node
		rendered.Title = renderString(rendered.Title, values)
		rendered.AssignedTo = renderString(rendered.AssignedTo, values)
		rendered.Config = renderMap(rendered.Config, values)
		out = append(out, rendered)
	}
	return out
}

func buildDAGDraft(tpl Template, req RenderRequest, values map[string]string, nodes []NodeTemplate, finalOutput FinalOutput) DAGDraft {
	return DAGDraft{
		TemplateID:      tpl.ID,
		TemplateVersion: tpl.Version,
		DAGKey:          renderString(tpl.DAGTemplate.DAGKeyTemplate, values),
		Title:           renderString(tpl.DAGTemplate.TitleTemplate, values),
		Description:     renderString(tpl.DAGTemplate.DescriptionTemplate, values),
		Trigger:         tpl.DAGTemplate.Trigger,
		FinalNodeKey:    tpl.DAGTemplate.FinalNodeKey,
		ReviewNodeKey:   reviewNodeKey(tpl),
		Nodes:           nodes,
		FinalOutput:     finalOutput,
		Metadata: map[string]any{
			"source":           "workflow_template",
			"template_id":      tpl.ID,
			"template_version": tpl.Version,
			"template_locale":  templateLocale(req),
		},
	}
}

func templateSummary(tpl Template) TemplateSummary {
	return TemplateSummary{
		ID:               tpl.ID,
		Version:          tpl.Version,
		Title:            tpl.Title,
		Description:      tpl.Description,
		Category:         tpl.Category,
		BusinessFlow:     tpl.BusinessFlow,
		OutputTypes:      append([]string(nil), tpl.OutputTypes...),
		Tags:             append([]string(nil), tpl.Tags...),
		EstimatedNodes:   tpl.EstimatedNodes,
		RequiresReview:   tpl.RequiresReview,
		SupportsSchedule: tpl.SupportsSchedule,
		FinalNodeKey:     tpl.DAGTemplate.FinalNodeKey,
	}
}

func firstListFilter(filters []ListFilter) ListFilter {
	if len(filters) == 0 {
		return ListFilter{}
	}
	return filters[0]
}

func templateSummaryMatches(tpl TemplateSummary, filter ListFilter) bool {
	if !matchesOptionalText(tpl.Category, filter.Category) {
		return false
	}
	if !matchesOptionalText(tpl.BusinessFlow, filter.BusinessFlow) {
		return false
	}
	if filter.OutputType != "" && !hasOutputType(tpl.OutputTypes, filter.OutputType) {
		return false
	}
	return filter.SupportsSchedule == nil || tpl.SupportsSchedule == *filter.SupportsSchedule
}

func matchesOptionalText(got string, want string) bool {
	want = strings.TrimSpace(want)
	return want == "" || got == want
}
