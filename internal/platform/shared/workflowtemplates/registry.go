package workflowtemplates

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed assets/manifest.json assets/government-enterprise/*.yaml
var embeddedAssets embed.FS

// Registry 负责加载和渲染内置工作流模板，保持只读且无数据库副作用。
type Registry struct {
	mu               sync.RWMutex                // 保护模板列表和版本索引。
	order            []string                    // manifest 顺序或首次保存顺序。
	templates        map[string]Template         // 当前活跃模板版本。
	templateVersions map[string]map[int]Template // 每个模板保留的历史版本。
	rules            validationRules             // 实例私有的模板校验白名单。
}

// manifest 对应内置模板 manifest.json，只声明允许从 embed.FS 读取的模板 YAML 路径。
type manifest struct {
	Version   int      `json:"version"`
	Templates []string `json:"templates"`
}

// NewDefaultRegistry 从仓库内置资源加载工作流模板，启动期校验失败会直接返回错误。
func NewDefaultRegistry() (*Registry, error) {
	return Load(embeddedAssets)
}

// Load 从文件系统加载 manifest 和模板资源，路径逃逸、重复 ID 或 schema 错误都会阻断。
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	filter := firstListFilter(filters)
	out := make([]TemplateSummary, 0, len(r.order))
	for _, id := range r.order {
		summary := r.templateSummaryLocked(r.templates[id])
		if templateSummaryMatches(summary, filter) {
			out = append(out, summary)
		}
	}
	return out
}

// GetTemplate 在读锁内取当前活跃模板定义；未命中时不触发默认模板或磁盘回退。
func (r *Registry) GetTemplate(id string) (Template, bool) {
	if r == nil {
		return Template{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tpl, ok := r.templates[strings.TrimSpace(id)]
	return tpl, ok
}

// SaveTemplate 校验并登记一个新模板版本，同一 ID/version 重复会 fail-fast。
func (r *Registry) SaveTemplate(tpl Template) error {
	if r == nil {
		return errors.New("workflowtemplates: registry is nil")
	}
	if err := validatePublishedTemplate(tpl, r.rules); err != nil {
		return fmt.Errorf("workflowtemplates: validate saved template: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id := strings.TrimSpace(tpl.ID)
	if _, exists := r.templateVersions[id]; !exists {
		r.order = append(r.order, id)
		r.templateVersions[id] = make(map[int]Template)
	}
	if _, exists := r.templateVersions[id][tpl.Version]; exists {
		return fmt.Errorf("workflowtemplates: template %q version %d already exists", id, tpl.Version)
	}
	r.templateVersions[id][tpl.Version] = tpl
	r.templates[id] = tpl
	return nil
}

// RollbackTemplate 将指定历史版本设为当前活跃版本，历史版本本身不被删除。
func (r *Registry) RollbackTemplate(id string, version int) error {
	if r == nil {
		return errors.New("workflowtemplates: registry is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.templateVersions[strings.TrimSpace(id)]
	if len(versions) == 0 {
		return fmt.Errorf("workflowtemplates: template %q not found", id)
	}
	tpl, ok := versions[version]
	if !ok {
		return fmt.Errorf("workflowtemplates: template %q version %d not found", id, version)
	}
	r.templates[strings.TrimSpace(id)] = tpl
	return nil
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
	if err := validateRuntimeNodeConfigs(nodes, r.rules); err != nil {
		return DAGDraft{}, err
	}
	if err := validateRenderedOutputPaths(tpl, nodes, finalOutput); err != nil {
		return DAGDraft{}, err
	}
	return buildDAGDraft(tpl, req, values, nodes, finalOutput), nil
}

// loadTemplates 按 manifest 顺序加载并校验所有模板，重复 ID 会阻断启动。
func loadTemplates(fsys fs.FS, items []string) (*Registry, error) {
	reg := &Registry{
		order:            make([]string, 0, len(items)),
		templates:        make(map[string]Template, len(items)),
		templateVersions: make(map[string]map[int]Template, len(items)),
		rules:            newValidationRules(),
	}
	for _, item := range items {
		tpl, err := loadTemplate(fsys, item)
		if err != nil {
			return nil, err
		}
		if err := validateTemplate(tpl, reg.rules); err != nil {
			return nil, fmt.Errorf("workflowtemplates: validate %s: %w", item, err)
		}
		if _, exists := reg.templates[tpl.ID]; exists {
			return nil, fmt.Errorf("workflowtemplates: duplicate template id %q", tpl.ID)
		}
		reg.order = append(reg.order, tpl.ID)
		reg.templates[tpl.ID] = tpl
		reg.templateVersions[tpl.ID] = map[int]Template{tpl.Version: tpl}
	}
	return reg, nil
}

// loadTemplate 读取单个 YAML 模板文件，并拒绝 manifest 中的路径逃逸。
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

// TemplateFromSaveRequest 从已渲染 DAG 草案构造模板定义，保存前仍会走完整 schema 校验。
func TemplateFromSaveRequest(req SaveTemplateRequest) (Template, error) {
	id := strings.TrimSpace(req.TemplateID)
	if id == "" {
		id = strings.TrimSpace(req.Draft.TemplateID)
	}
	if id == "" {
		return Template{}, errors.New("workflowtemplates: template_id is required")
	}
	if req.Version <= 0 {
		return Template{}, errors.New("workflowtemplates: version must be a positive integer")
	}
	if len(req.Draft.Nodes) == 0 {
		return Template{}, errors.New("workflowtemplates: draft.nodes is required")
	}
	if strings.TrimSpace(req.Draft.FinalNodeKey) == "" {
		return Template{}, errors.New("workflowtemplates: draft.final_node_key is required")
	}
	finalOutput := req.Draft.FinalOutput
	if strings.TrimSpace(finalOutput.NodeKey) == "" {
		finalOutput.NodeKey = req.Draft.FinalNodeKey
	}
	return Template{
		ID:               id,
		Version:          req.Version,
		Title:            req.Title,
		Description:      req.Description,
		Category:         req.Category,
		BusinessFlow:     req.BusinessFlow,
		OutputTypes:      append([]string(nil), req.OutputTypes...),
		Tags:             append([]string(nil), req.Tags...),
		EstimatedNodes:   len(req.Draft.Nodes),
		RequiresReview:   req.RequiresReview,
		SupportsSchedule: req.SupportsSchedule,
		Trust:            req.Trust,
		Compatibility:    req.Compatibility,
		UISchema:         append([]UIField(nil), req.UISchema...),
		DAGTemplate: DAGTemplate{
			DAGKeyTemplate:      req.Draft.DAGKey,
			TitleTemplate:       req.Draft.Title,
			DescriptionTemplate: req.Draft.Description,
			Trigger:             req.Draft.Trigger,
			FinalNodeKey:        req.Draft.FinalNodeKey,
			Nodes:               append([]NodeTemplate(nil), req.Draft.Nodes...),
		},
		Validation:  req.Validation,
		FinalOutput: finalOutput,
	}, nil
}

// renderNodes 渲染节点标题、负责人和配置，同时补齐 agent 节点 exec.cwd。
func renderNodes(nodes []NodeTemplate, values map[string]string) []NodeTemplate {
	out := make([]NodeTemplate, 0, len(nodes))
	for _, node := range nodes {
		rendered := node
		rendered.Title = renderString(rendered.Title, values)
		rendered.AssignedTo = renderString(rendered.AssignedTo, values)
		rendered.Config = renderMap(rendered.Config, values)
		ensureAgentExecCWD(&rendered, values["cwd"])
		out = append(out, rendered)
	}
	return out
}

// ensureAgentExecCWD 为 agent 节点补齐 exec.cwd，避免运行时节点缺工作目录。
func ensureAgentExecCWD(node *NodeTemplate, cwd string) {
	if node == nil || strings.TrimSpace(strings.ToLower(node.NodeType)) != "agent" {
		return
	}
	if node.Config == nil {
		node.Config = make(map[string]any)
	}
	exec, _ := objectMap(node.Config["exec"])
	if exec == nil {
		exec = make(map[string]any)
	}
	if value := strings.TrimSpace(fmt.Sprint(exec["cwd"])); value != "" && value != "<nil>" {
		node.Config["exec"] = exec
		return
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		cwd = "{{cwd}}"
	}
	exec["cwd"] = cwd
	node.Config["exec"] = exec
}

// buildDAGDraft 组装只读 DAG 草案，不写入 DAG 存储也不启动节点。
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

// templateSummaryLocked 在调用方持锁时构造模板摘要，包含可回滚版本列表。
func (r *Registry) templateSummaryLocked(tpl Template) TemplateSummary {
	return TemplateSummary{
		ID:                tpl.ID,
		Version:           tpl.Version,
		Title:             tpl.Title,
		Description:       tpl.Description,
		Category:          tpl.Category,
		BusinessFlow:      tpl.BusinessFlow,
		OutputTypes:       append([]string(nil), tpl.OutputTypes...),
		Tags:              append([]string(nil), tpl.Tags...),
		EstimatedNodes:    tpl.EstimatedNodes,
		RequiresReview:    tpl.RequiresReview,
		SupportsSchedule:  tpl.SupportsSchedule,
		FinalNodeKey:      tpl.DAGTemplate.FinalNodeKey,
		Trust:             tpl.Trust,
		Compatibility:     tpl.Compatibility,
		AvailableVersions: sortedTemplateVersions(r.templateVersions[tpl.ID]),
	}
}

// sortedTemplateVersions 生成目录 API 使用的稳定版本列表，nil map 会返回空切片。
func sortedTemplateVersions(versions map[int]Template) []int {
	out := make([]int, 0, len(versions))
	for version := range versions {
		out = append(out, version)
	}
	sort.Ints(out)
	return out
}

// firstListFilter 兼容可变参数调用，只使用第一个筛选条件。
func firstListFilter(filters []ListFilter) ListFilter {
	if len(filters) == 0 {
		return ListFilter{}
	}
	return filters[0]
}

// templateSummaryMatches 判断模板摘要是否满足目录筛选条件，空筛选值表示不限制。
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

// matchesOptionalText 在筛选值为空时视为匹配。
func matchesOptionalText(got string, want string) bool {
	want = strings.TrimSpace(want)
	return want == "" || got == want
}
