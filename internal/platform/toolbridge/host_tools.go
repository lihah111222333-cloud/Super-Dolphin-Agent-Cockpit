package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared/workflowtemplates"
)

// HostToolCall 是 host-direct 工具调用的内部 wire 结构。
// Arguments 来自模型 JSON，cwd/agent/thread/call 元数据由 Handler 注入，不能由模型伪造。
type HostToolCall struct {
	Name      string
	Arguments json.RawMessage
	CWD       string
	AgentID   string
	ThreadID  string
	TurnID    string
	CallID    string
}

// HostToolRegistry 暴露由 host 进程直接执行的工具集合，不经过 mcp-orch 或 mcp-lsp peer。
// nil registry 表示当前图没有 host-direct 工具，列表和调用路径都必须保持 nil-safe。
type HostToolRegistry interface {
	// ListHostTools 列出 host 直跑的工具，结果会与 peer 工具合并送给模型。
	ListHostTools() []mcpdto.MCPTool
	// HasTool 判断给定工具名是否由本 registry 处理。routeToolCall 用它做分支
	// 决策，避免把 skill_* 工具误投到 peer。
	HasTool(name string) bool
	// CallHostTool 同进程执行工具调用。cwd 由 Handler 从 thread context 解析后注入，
	// 不暴露给模型；arguments 是模型填的 JSON，只允许 schema 定义的字段。
	CallHostTool(ctx context.Context, call HostToolCall) (any, error)
}

// HostToolCWDPolicy 允许 registry 声明某个 host-direct 工具是否必须绑定 cwd。
// 默认策略要求 cwd；只读或全局工具可显式豁免，避免无关工作区缺失阻断调用。
type HostToolCWDPolicy interface {
	RequiresCWD(name string) bool
}

// 已下线 skill 工具名保留在这里，只用于拒绝旧 Codex 调用和同名 MCP peer。
// 实现不再存在，命中时必须返回明确失败，不能静默转投其它工具。
const (
	// skill_read_section 已从生产路径下线，此常量仅用于向后兼容拒绝响应，不是 skill 发现入口。
	ToolNameReadSection             = "skill_read_section"
	ToolNameLegacySkillExpandBody   = "skill_expand_body"
	ToolNameLegacySkillReadResource = "skill_read_resource"
)

// workflow template host-direct 工具名。
const (
	ToolNameWorkflowTemplateList      = "workflow_template_list"
	ToolNameWorkflowTemplateGet       = "workflow_template_get"
	ToolNameWorkflowTemplateRenderDAG = "workflow_template_render_dag"
	ToolNameWorkflowTemplateSave      = "workflow_template_save"
	ToolNameWorkflowTemplateRollback  = "workflow_template_rollback"
)

// WorkflowTemplateHostToolRegistry 将内置工作流模板库暴露为 host-direct 工具。
// list/get/render 是只读入口，save/rollback 只改模板资产，不直接创建或运行 DAG。
type WorkflowTemplateHostToolRegistry struct {
	registry *workflowtemplates.Registry
	loadErr  error
}

// WorkflowTemplateWriteAuthority 是写入模板资产前必须通过的外部授权边界。
// 默认生产图不装配写 registry；只有显式具备 admin/developer 能力或审批管理器时才能注入实现。
type WorkflowTemplateWriteAuthority interface {
	AuthorizeWorkflowTemplateWrite(context.Context, HostToolCall) error
}

// WorkflowTemplateWriteHostToolRegistry 暴露 save/rollback 写入口。
// 它与默认只读 registry 分离，避免普通 Codex 动态工具面获得模板写能力。
type WorkflowTemplateWriteHostToolRegistry struct {
	*WorkflowTemplateHostToolRegistry
	authority WorkflowTemplateWriteAuthority
}

// workflowTemplateListInput 是 workflow_template_list 的模型输入，兼容筛选维度来自 JSON schema。
type workflowTemplateListInput struct {
	Category         string `json:"category,omitempty"`
	BusinessFlow     string `json:"business_flow,omitempty"`
	OutputType       string `json:"output_type,omitempty"`
	SupportsSchedule *bool  `json:"supports_schedule,omitempty"`
	Locale           string `json:"locale,omitempty"`
}

// workflowTemplateGetInput 是 workflow_template_get 的模型输入。
// templateId/id 兼容前端与旧调用形态，内部会统一为 template_id。
type workflowTemplateGetInput struct {
	TemplateID      string `json:"template_id,omitempty"`
	TemplateIDCamel string `json:"templateId,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         any    `json:"version,omitempty"`
	Locale          string `json:"locale,omitempty"`
}

// workflowTemplateRenderInput 是 workflow_template_render_dag 的模型输入。
// values/user_inputs 都保留用于兼容不同模板调用方，渲染结果只返回草稿。
type workflowTemplateRenderInput struct {
	TemplateID      string         `json:"template_id,omitempty"`
	TemplateIDCamel string         `json:"templateId,omitempty"`
	ID              string         `json:"id,omitempty"`
	Version         any            `json:"version,omitempty"`
	Values          map[string]any `json:"values,omitempty"`
	UserInputs      map[string]any `json:"user_inputs,omitempty"`
	RuntimeContext  map[string]any `json:"runtime_context,omitempty"`
}

// workflowTemplateSaveInput 是 workflow_template_save 的模型输入。
// 字段对应模板资产的版本化存储边界，保存模板不会创建任务或启动 DAG。
type workflowTemplateSaveInput struct {
	TemplateID       string                           `json:"template_id,omitempty"`
	TemplateIDCamel  string                           `json:"templateId,omitempty"`
	Version          int                              `json:"version,omitempty"`
	Title            workflowtemplates.LocalizedText  `json:"title"`
	Description      workflowtemplates.LocalizedText  `json:"description"`
	Category         string                           `json:"category,omitempty"`
	BusinessFlow     string                           `json:"business_flow,omitempty"`
	OutputTypes      []string                         `json:"output_types,omitempty"`
	Tags             []string                         `json:"tags,omitempty"`
	RequiresReview   bool                             `json:"requires_review,omitempty"`
	SupportsSchedule bool                             `json:"supports_schedule,omitempty"`
	Trust            workflowtemplates.TrustMetadata  `json:"trust"`
	Compatibility    workflowtemplates.Compatibility  `json:"compatibility"`
	UISchema         []workflowtemplates.UIField      `json:"ui_schema,omitempty"`
	Validation       workflowtemplates.ValidationRule `json:"validation"`
	Draft            workflowtemplates.DAGDraft       `json:"draft"`
}

// workflowTemplateRollbackInput 是 workflow_template_rollback 的模型输入。
// templateId/id 兼容旧字段名，version 必须显式给出以避免回滚到不确定版本。
type workflowTemplateRollbackInput struct {
	TemplateID      string `json:"template_id,omitempty"`
	TemplateIDCamel string `json:"templateId,omitempty"`
	ID              string `json:"id,omitempty"`
	Version         int    `json:"version,omitempty"`
}

// workflowTemplateListResult 是模板列表工具返回给模型的 wire 外壳。
type workflowTemplateListResult struct {
	Templates []workflowtemplates.TemplateSummary `json:"templates"`
}

// workflowTemplateGetResult 是模板详情工具返回给模型的 wire 外壳。
type workflowTemplateGetResult struct {
	Template workflowtemplates.Template `json:"template"`
}

// workflowTemplateRenderResult 是渲染工具返回 DAG 草稿的 wire 外壳。
type workflowTemplateRenderResult struct {
	Draft workflowtemplates.DAGDraft `json:"draft"`
}

// workflowTemplateSaveResult 是保存工具返回新模板摘要的 wire 外壳。
type workflowTemplateSaveResult struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

// workflowTemplateRollbackResult 是回滚工具返回当前激活版本摘要的 wire 外壳。
type workflowTemplateRollbackResult struct {
	Template workflowtemplates.TemplateSummary `json:"template"`
}

// NewWorkflowTemplateHostToolRegistry 创建只读模板工具注册表，供 DAG Designer 读取同一份内置模板资产。
func NewWorkflowTemplateHostToolRegistry(registry *workflowtemplates.Registry) *WorkflowTemplateHostToolRegistry {
	if registry == nil {
		return &WorkflowTemplateHostToolRegistry{loadErr: fmt.Errorf("workflow template registry is not configured")}
	}
	return &WorkflowTemplateHostToolRegistry{registry: registry}
}

// NewWorkflowTemplateWriteHostToolRegistry 创建受授权保护的模板写工具注册表。
func NewWorkflowTemplateWriteHostToolRegistry(registry *workflowtemplates.Registry, authority WorkflowTemplateWriteAuthority) *WorkflowTemplateWriteHostToolRegistry {
	return &WorkflowTemplateWriteHostToolRegistry{
		WorkflowTemplateHostToolRegistry: NewWorkflowTemplateHostToolRegistry(registry),
		authority:                        authority,
	}
}

// ListHostTools 返回模板库的 list/get/render 三个只读工具，不创建 DAG、不写文件。
func (r *WorkflowTemplateHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	listSchema, _ := json.Marshal(workflowTemplateListInputSchema())
	getSchema, _ := json.Marshal(workflowTemplateGetInputSchema())
	renderSchema, _ := json.Marshal(workflowTemplateRenderInputSchema())
	return []mcpdto.MCPTool{
		{Name: ToolNameWorkflowTemplateList, Description: descriptionWorkflowTemplateList, InputSchema: listSchema},
		{Name: ToolNameWorkflowTemplateGet, Description: descriptionWorkflowTemplateGet, InputSchema: getSchema},
		{Name: ToolNameWorkflowTemplateRenderDAG, Description: descriptionWorkflowTemplateRenderDAG, InputSchema: renderSchema},
	}
}

// HasTool 判断工具名是否由政企模板库注册表处理。
func (r *WorkflowTemplateHostToolRegistry) HasTool(name string) bool {
	return r != nil && isWorkflowTemplateReadToolName(name)
}

// RequiresCWD 声明模板库工具不依赖工作目录，避免无关 cwd 缺失阻断模板读取。
func (r *WorkflowTemplateHostToolRegistry) RequiresCWD(name string) bool {
	return !isWorkflowTemplateReadToolName(name)
}

// CallHostTool 执行模板库只读工具；渲染只返回 DAG 草稿，不落库、不启动运行。
func (r *WorkflowTemplateHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("workflow template tools are not configured")
	}
	if r.loadErr != nil {
		return nil, fmt.Errorf("workflow template registry unavailable: %w", r.loadErr)
	}
	switch strings.TrimSpace(call.Name) {
	case ToolNameWorkflowTemplateList:
		return r.list(call.Arguments)
	case ToolNameWorkflowTemplateGet:
		return r.get(call.Arguments)
	case ToolNameWorkflowTemplateRenderDAG:
		return r.renderDAG(call.Arguments)
	case ToolNameWorkflowTemplateSave:
		return nil, workflowTemplateWriteApprovalRequired(ctx, call)
	case ToolNameWorkflowTemplateRollback:
		return nil, workflowTemplateWriteApprovalRequired(ctx, call)
	default:
		return nil, fmt.Errorf("workflow template tools: unknown tool %q", call.Name)
	}
}

// ListHostTools 返回模板写工具 schema；该 registry 只应在授权边界明确存在时装配。
func (r *WorkflowTemplateWriteHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	if r == nil {
		return nil
	}
	saveSchema, _ := json.Marshal(workflowTemplateSaveInputSchema())
	rollbackSchema, _ := json.Marshal(workflowTemplateRollbackInputSchema())
	return []mcpdto.MCPTool{
		{Name: ToolNameWorkflowTemplateSave, Description: descriptionWorkflowTemplateSave, InputSchema: saveSchema},
		{Name: ToolNameWorkflowTemplateRollback, Description: descriptionWorkflowTemplateRollback, InputSchema: rollbackSchema},
	}
}

// HasTool 判断给定名称是否属于受保护的模板写工具。
func (r *WorkflowTemplateWriteHostToolRegistry) HasTool(name string) bool {
	return r != nil && isWorkflowTemplateWriteToolName(name)
}

// RequiresCWD 声明模板写工具不依赖工作目录；权限由 authority 单独判断。
func (r *WorkflowTemplateWriteHostToolRegistry) RequiresCWD(name string) bool {
	return !isWorkflowTemplateWriteToolName(name)
}

// CallHostTool 在授权通过后执行模板保存或回滚；缺少 authority 时返回审批请求。
func (r *WorkflowTemplateWriteHostToolRegistry) CallHostTool(ctx context.Context, call HostToolCall) (any, error) {
	if r == nil {
		return nil, fmt.Errorf("workflow template write tools are not configured")
	}
	if r.loadErr != nil {
		return nil, fmt.Errorf("workflow template registry unavailable: %w", r.loadErr)
	}
	if r.authority == nil {
		return nil, workflowTemplateWriteApprovalRequired(ctx, call)
	}
	if err := r.authority.AuthorizeWorkflowTemplateWrite(ctx, call); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(call.Name) {
	case ToolNameWorkflowTemplateSave:
		return r.save(call.Arguments)
	case ToolNameWorkflowTemplateRollback:
		return r.rollback(call.Arguments)
	default:
		return nil, fmt.Errorf("workflow template write tools: unknown tool %q", call.Name)
	}
}

// isWorkflowTemplateReadToolName 判断工具名是否属于默认只读模板工具集合。
func isWorkflowTemplateReadToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameWorkflowTemplateList, ToolNameWorkflowTemplateGet, ToolNameWorkflowTemplateRenderDAG:
		return true
	default:
		return false
	}
}

// isWorkflowTemplateWriteToolName 判断工具名是否属于需要授权的模板写工具集合。
func isWorkflowTemplateWriteToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameWorkflowTemplateSave, ToolNameWorkflowTemplateRollback:
		return true
	default:
		return false
	}
}

// workflowTemplateWriteApprovalRequired 构造模板写工具的审批请求，阻断未授权调用。
func workflowTemplateWriteApprovalRequired(_ context.Context, call HostToolCall) error {
	toolName := strings.TrimSpace(call.Name)
	return contract.SkillApprovalRequiredError{Request: contract.ApprovalRequest{
		CallID:       strings.TrimSpace(call.CallID),
		ToolName:     toolName,
		AgentID:      strings.TrimSpace(call.AgentID),
		ThreadID:     strings.TrimSpace(call.ThreadID),
		TurnID:       strings.TrimSpace(call.TurnID),
		Reason:       "workflow template write requires admin/developer approval",
		Kind:         "workflow_template_write",
		SourceMethod: toolName,
		Payload: map[string]any{
			"tool":                toolName,
			"requires_capability": "admin_or_developer",
		},
	}}
}

// list 解码筛选条件并返回模板摘要；输入 JSON 使用严格解码，未知字段直接失败。
func (r *WorkflowTemplateHostToolRegistry) list(raw json.RawMessage) (workflowTemplateListResult, error) {
	var input workflowTemplateListInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateListResult{}, err
	}
	templates := r.registry.ListTemplates(workflowtemplates.ListFilter{
		Category:         input.Category,
		BusinessFlow:     input.BusinessFlow,
		OutputType:       input.OutputType,
		SupportsSchedule: input.SupportsSchedule,
	})
	return workflowTemplateListResult{Templates: templates}, nil
}

// get 按兼容字段解析模板 ID 和版本，返回完整模板定义。
func (r *WorkflowTemplateHostToolRegistry) get(raw json.RawMessage) (workflowTemplateGetResult, error) {
	var input workflowTemplateGetInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateGetResult{}, err
	}
	tpl, err := r.getTemplateByInput(input.TemplateID, input.TemplateIDCamel, input.ID, input.Version)
	if err != nil {
		return workflowTemplateGetResult{}, err
	}
	return workflowTemplateGetResult{Template: tpl}, nil
}

// renderDAG 把模板和模型输入渲染成 DAG 草稿；这里只返回草稿，不持久化也不启动执行。
func (r *WorkflowTemplateHostToolRegistry) renderDAG(raw json.RawMessage) (workflowTemplateRenderResult, error) {
	var input workflowTemplateRenderInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateRenderResult{}, err
	}
	tpl, err := r.getTemplateByInput(input.TemplateID, input.TemplateIDCamel, input.ID, input.Version)
	if err != nil {
		return workflowTemplateRenderResult{}, err
	}
	values := input.Values
	if len(values) == 0 {
		values = input.UserInputs
	}
	draft, err := r.registry.RenderDAGDraft(workflowtemplates.RenderRequest{
		TemplateID:     tpl.ID,
		Version:        input.Version,
		Values:         values,
		UserInputs:     input.UserInputs,
		RuntimeContext: input.RuntimeContext,
	})
	if err != nil {
		return workflowTemplateRenderResult{}, err
	}
	return workflowTemplateRenderResult{Draft: draft}, nil
}

// save 将模型传入的模板草稿转换为版本化模板资产并写入 registry。
// 保存失败会直接返回错误，调用方不会得到半成功状态。
func (r *WorkflowTemplateHostToolRegistry) save(raw json.RawMessage) (workflowTemplateSaveResult, error) {
	var input workflowTemplateSaveInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateSaveResult{}, err
	}
	id := firstWorkflowTemplateID(input.TemplateID, input.TemplateIDCamel)
	tpl, err := workflowtemplates.TemplateFromSaveRequest(workflowtemplates.SaveTemplateRequest{
		TemplateID:       id,
		Version:          input.Version,
		Title:            input.Title,
		Description:      input.Description,
		Category:         input.Category,
		BusinessFlow:     input.BusinessFlow,
		OutputTypes:      append([]string(nil), input.OutputTypes...),
		Tags:             append([]string(nil), input.Tags...),
		RequiresReview:   input.RequiresReview,
		SupportsSchedule: input.SupportsSchedule,
		Trust:            input.Trust,
		Compatibility:    input.Compatibility,
		UISchema:         append([]workflowtemplates.UIField(nil), input.UISchema...),
		Validation:       input.Validation,
		Draft:            input.Draft,
	})
	if err != nil {
		return workflowTemplateSaveResult{}, err
	}
	if err := r.registry.SaveTemplate(tpl); err != nil {
		return workflowTemplateSaveResult{}, err
	}
	summary, err := r.summaryByID(tpl.ID)
	if err != nil {
		return workflowTemplateSaveResult{}, err
	}
	return workflowTemplateSaveResult{Template: summary}, nil
}

// rollback 将指定模板切回显式版本，并返回回滚后的模板摘要。
// ID 或版本缺失都会 fail-fast，避免模型触发不确定回滚。
func (r *WorkflowTemplateHostToolRegistry) rollback(raw json.RawMessage) (workflowTemplateRollbackResult, error) {
	var input workflowTemplateRollbackInput
	if err := decodeWorkflowTemplateToolInput(raw, &input); err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	id := firstWorkflowTemplateID(input.TemplateID, input.TemplateIDCamel, input.ID)
	if id == "" {
		return workflowTemplateRollbackResult{}, fmt.Errorf("workflow template input: template_id is required")
	}
	if input.Version <= 0 {
		return workflowTemplateRollbackResult{}, fmt.Errorf("workflow template rollback: version must be a positive integer")
	}
	if err := r.registry.RollbackTemplate(id, input.Version); err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	summary, err := r.summaryByID(id)
	if err != nil {
		return workflowTemplateRollbackResult{}, err
	}
	return workflowTemplateRollbackResult{Template: summary}, nil
}

// summaryByID 从 registry 当前列表中查找模板摘要，供保存和回滚后回传一致的模型结果。
func (r *WorkflowTemplateHostToolRegistry) summaryByID(id string) (workflowtemplates.TemplateSummary, error) {
	for _, summary := range r.registry.ListTemplates() {
		if summary.ID == id {
			return summary, nil
		}
	}
	return workflowtemplates.TemplateSummary{}, fmt.Errorf("workflow template %q summary not found", id)
}

// firstWorkflowTemplateID 返回第一个非空模板 ID，按新旧字段优先级保持兼容。
func firstWorkflowTemplateID(ids ...string) string {
	for _, id := range ids {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// getTemplateByInput 从 template_id/templateId/id/version 兼容字段中定位模板。
// 找不到模板或版本不匹配时返回明确错误，避免渲染错版本。
func (r *WorkflowTemplateHostToolRegistry) getTemplateByInput(ids ...any) (workflowtemplates.Template, error) {
	id, version := workflowTemplateIDAndVersion(ids...)
	if id == "" {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template input: template_id is required")
	}
	tpl, ok := r.registry.GetTemplate(id)
	if !ok {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template %q not found", id)
	}
	if version != "" && version != strconv.Itoa(tpl.Version) {
		return workflowtemplates.Template{}, fmt.Errorf("workflow template %q version %s not found", id, version)
	}
	return tpl, nil
}

// workflowTemplateIDAndVersion 从混合类型字段中抽取模板 ID 和版本字符串。
// 前三个位置按 ID 兼容字段处理，之后的位置视为版本。
func workflowTemplateIDAndVersion(values ...any) (string, string) {
	var id string
	for i, value := range values {
		text := workflowTemplateVersionString(value)
		if i < 3 && id == "" {
			id = text
			continue
		}
		if i >= 3 {
			return id, text
		}
	}
	return id, ""
}

// workflowTemplateVersionString 将模型输入中的字符串、数字或 json.Number 统一为版本字符串。
func workflowTemplateVersionString(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	default:
		if value == nil {
			return ""
		}
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

// workflowTemplateHasOutputType 判断模板输出类型列表是否包含目标类型。
func workflowTemplateHasOutputType(outputTypes []string, want string) bool {
	for _, outputType := range outputTypes {
		if strings.TrimSpace(outputType) == want {
			return true
		}
	}
	return false
}

// decodeWorkflowTemplateToolInput 严格解码模板工具输入。
// 空输入按空对象处理；未知字段或尾随 JSON 都会报错，避免模型参数被静默吞掉。
func decodeWorkflowTemplateToolInput(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("invalid workflow template tool input: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid workflow template tool input: trailing JSON")
	}
	return nil
}

// workflowTemplateListInputSchema 定义列表工具对模型可见的输入 schema。
func workflowTemplateListInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"category":      map[string]any{"type": "string"},
			"business_flow": map[string]any{"type": "string"},
			"output_type":   map[string]any{"type": "string", "enum": []string{"video", "pptx", "docx", "xlsx", "markdown", "pdf", "json", "md", "mp4"}},
			"supports_schedule": map[string]any{
				"type": "boolean",
			},
			"locale": map[string]any{"type": "string"},
		},
		"additionalProperties": false,
	}
}

// workflowTemplateGetInputSchema 定义详情工具的输入 schema，并保留 templateId/id 兼容字段。
func workflowTemplateGetInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": []string{"string", "number"}},
			"locale":      map[string]any{"type": "string"},
		},
		"required":             []string{"template_id"},
		"additionalProperties": false,
	}
}

// workflowTemplateRenderInputSchema 定义渲染工具的输入 schema。
// runtime_context 只是渲染提示，不会导致工具持久化或运行 DAG。
func workflowTemplateRenderInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": []string{"string", "number"}},
			"values":      map[string]any{"type": "object"},
			"user_inputs": map[string]any{"type": "object"},
			"runtime_context": map[string]any{
				"type":        "object",
				"description": "Optional runtime hints; this read-only tool does not create or run DAGs.",
			},
		},
		"required":             []string{"template_id"},
		"additionalProperties": false,
	}
}

// workflowTemplateSaveInputSchema 定义保存工具的输入 schema，要求关键治理字段齐全。
func workflowTemplateSaveInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id":       map[string]any{"type": "string"},
			"templateId":        map[string]any{"type": "string"},
			"version":           map[string]any{"type": "integer", "minimum": 1},
			"title":             map[string]any{"type": "object"},
			"description":       map[string]any{"type": "object"},
			"category":          map[string]any{"type": "string"},
			"business_flow":     map[string]any{"type": "string"},
			"output_types":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"tags":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"requires_review":   map[string]any{"type": "boolean"},
			"supports_schedule": map[string]any{"type": "boolean"},
			"trust":             map[string]any{"type": "object"},
			"compatibility":     map[string]any{"type": "object"},
			"ui_schema":         map[string]any{"type": "array"},
			"validation":        map[string]any{"type": "object"},
			"draft":             map[string]any{"type": "object"},
		},
		"required":             []string{"template_id", "version", "category", "compatibility", "trust", "draft"},
		"additionalProperties": false,
	}
}

// workflowTemplateRollbackInputSchema 定义回滚工具的输入 schema，强制指定版本。
func workflowTemplateRollbackInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"template_id": map[string]any{"type": "string"},
			"templateId":  map[string]any{"type": "string"},
			"id":          map[string]any{"type": "string"},
			"version":     map[string]any{"type": "integer", "minimum": 1},
		},
		"required":             []string{"template_id", "version"},
		"additionalProperties": false,
	}
}

// 模板工具给模型看的简短说明；这些是 wire 文案，不参与 Go 注释守卫。
const descriptionWorkflowTemplateList = "List built-in government-enterprise workflow template summaries. Read-only; does not create DAGs or write files."
const descriptionWorkflowTemplateGet = "Get one built-in government-enterprise workflow template with ui_schema, dag_template, review node, and final output contract."
const descriptionWorkflowTemplateRenderDAG = "Render a built-in workflow template into a DAG draft from values/user_inputs. Read-only; does not persist or run the DAG."
const descriptionWorkflowTemplateSave = "Save a validated DAG draft as a versioned reusable workflow template. Does not create or run DAGs."
const descriptionWorkflowTemplateRollback = "Make a previous workflow template version active again. Does not create or run DAGs."
