package workflowtemplates

// LocalizedText 是模板 JSON/YAML 与前端列表共用的多语言文案载体。
type LocalizedText struct {
	Zh string `json:"zh" yaml:"zh"`
	En string `json:"en,omitempty" yaml:"en,omitempty"`
}

// UIOption 是 select/multi_select 字段在 wire payload 中的稳定选项。
type UIOption struct {
	Value string        `json:"value" yaml:"value"`
	Label LocalizedText `json:"label" yaml:"label"`
}

// UIField 定义模板参数表单的跨模块 schema，前端渲染和后端校验共享同一组 key。
type UIField struct {
	Key         string        `json:"key" yaml:"key"`
	Type        string        `json:"type" yaml:"type"`
	Required    bool          `json:"required" yaml:"required"`
	Label       LocalizedText `json:"label" yaml:"label"`
	Placeholder LocalizedText `json:"placeholder" yaml:"placeholder"`
	Help        LocalizedText `json:"help" yaml:"help"`
	Options     []UIOption    `json:"options,omitempty" yaml:"options"`
}

// Template 是内置模板的完整 wire 定义，加载器会按该结构解析 YAML 并生成运行草案。
type Template struct {
	ID               string         `json:"id" yaml:"id"`
	Version          int            `json:"version" yaml:"version"`
	Title            LocalizedText  `json:"title" yaml:"title"`
	Description      LocalizedText  `json:"description" yaml:"description"`
	Category         string         `json:"category" yaml:"category"`
	BusinessFlow     string         `json:"business_flow" yaml:"business_flow"`
	OutputTypes      []string       `json:"output_types" yaml:"output_types"`
	Tags             []string       `json:"tags" yaml:"tags"`
	EstimatedNodes   int            `json:"estimated_nodes" yaml:"estimated_nodes"`
	RequiresReview   bool           `json:"requires_review" yaml:"requires_review"`
	SupportsSchedule bool           `json:"supports_schedule" yaml:"supports_schedule"`
	Trust            TrustMetadata  `json:"trust" yaml:"trust"`
	Compatibility    Compatibility  `json:"compatibility" yaml:"compatibility"`
	UISchema         []UIField      `json:"ui_schema" yaml:"ui_schema"`
	DAGTemplate      DAGTemplate    `json:"dag_template" yaml:"dag_template"`
	Validation       ValidationRule `json:"validation" yaml:"validation"`
	FinalOutput      FinalOutput    `json:"final_output" yaml:"final_output"`
}

// TrustMetadata 在模板列表和保存请求中保留来源，避免内置模板与用户版本混用。
type TrustMetadata struct {
	Level  string `json:"level" yaml:"level"`
	Source string `json:"source" yaml:"source"`
}

// Compatibility 声明模板依赖的 runtime 能力，保存和渲染前都要用它做 fail-fast 校验。
type Compatibility struct {
	Runtime              string   `json:"runtime" yaml:"runtime"`
	NodeTypes            []string `json:"node_types" yaml:"node_types"`
	RequiredCapabilities []string `json:"required_capabilities" yaml:"required_capabilities"`
}

// TemplateSummary 是目录 API 的轻量返回体，只暴露列表筛选和预览需要的字段。
type TemplateSummary struct {
	ID                string        `json:"id"`
	Version           int           `json:"version"`
	Title             LocalizedText `json:"title"`
	Description       LocalizedText `json:"description"`
	Category          string        `json:"category"`
	BusinessFlow      string        `json:"business_flow"`
	OutputTypes       []string      `json:"output_types"`
	Tags              []string      `json:"tags"`
	EstimatedNodes    int           `json:"estimated_nodes"`
	RequiresReview    bool          `json:"requires_review"`
	SupportsSchedule  bool          `json:"supports_schedule"`
	FinalNodeKey      string        `json:"final_node_key"`
	Trust             TrustMetadata `json:"trust"`
	Compatibility     Compatibility `json:"compatibility"`
	AvailableVersions []int         `json:"available_versions"`
}

// ListFilter 是模板目录查询的进程内筛选条件，不参与 JSON wire 兼容。
type ListFilter struct {
	Category         string
	BusinessFlow     string
	OutputType       string
	SupportsSchedule *bool
}

// DAGTemplate 保存可参数化的 DAG 定义模板，渲染阶段才会替换模板变量。
type DAGTemplate struct {
	DAGKeyTemplate      string         `json:"dag_key_template" yaml:"dag_key_template"`
	TitleTemplate       string         `json:"title_template" yaml:"title_template"`
	DescriptionTemplate string         `json:"description_template" yaml:"description_template"`
	Trigger             string         `json:"trigger" yaml:"trigger"`
	FinalNodeKey        string         `json:"final_node_key" yaml:"final_node_key"`
	Nodes               []NodeTemplate `json:"nodes" yaml:"nodes"`
}

// NodeTemplate 是模板节点的 wire 形态；config 保持开放对象以兼容 DAG 节点 schema。
type NodeTemplate struct {
	NodeKey    string         `json:"node_key" yaml:"node_key"`
	Title      string         `json:"title" yaml:"title"`
	NodeType   string         `json:"node_type" yaml:"node_type"`
	AssignedTo string         `json:"assigned_to" yaml:"assigned_to"`
	DependsOn  []string       `json:"depends_on" yaml:"depends_on"`
	Config     map[string]any `json:"config" yaml:"config"`
}

// ValidationRule 汇总模板级 fail-fast 约束，防止渲染出缺少共享文件或终态节点的 DAG。
type ValidationRule struct {
	SharedFilePrefix         string   `json:"sharedfile_prefix,omitempty" yaml:"sharedfile_prefix"`
	SharedFilePrefixes       []string `json:"sharedfile_prefixes,omitempty" yaml:"sharedfile_prefixes"`
	RequireReviewBeforeFinal bool     `json:"require_review_before_final" yaml:"require_review_before_final"`
	RequireFinalNodeKey      bool     `json:"require_final_node_key" yaml:"require_final_node_key"`
}

// FinalOutput 绑定最终交付节点和路径模板，调用方据此定位产物但不直接写文件。
type FinalOutput struct {
	NodeKey      string `json:"node_key" yaml:"node_key"`
	Kind         string `json:"kind" yaml:"kind"`
	PathTemplate string `json:"path_template" yaml:"path_template"`
}

// RenderRequest 是渲染 API 的入参，兼容新旧 user_inputs/values 两种参数来源。
type RenderRequest struct {
	TemplateID     string         `json:"template_id"`
	Version        any            `json:"version,omitempty"`
	Values         map[string]any `json:"values,omitempty"`
	UserInputs     map[string]any `json:"user_inputs,omitempty"`
	RuntimeContext map[string]any `json:"runtime_context,omitempty"`
	TemplateLocale string         `json:"template_locale,omitempty"`
}

// DAGDraft 是模板渲染后的预览结果，保存前不会写入 DAG 运行态存储。
type DAGDraft struct {
	TemplateID      string         `json:"template_id"`
	TemplateVersion int            `json:"template_version"`
	DAGKey          string         `json:"dag_key"`
	Title           string         `json:"title"`
	Description     string         `json:"description"`
	Trigger         string         `json:"trigger"`
	FinalNodeKey    string         `json:"final_node_key"`
	ReviewNodeKey   string         `json:"review_node_key"`
	Nodes           []NodeTemplate `json:"nodes"`
	FinalOutput     FinalOutput    `json:"final_output"`
	Metadata        map[string]any `json:"metadata"`
}

// SaveTemplateRequest 是保存模板版本的 wire 请求，只持久化模板定义而不触碰运行态 run/node。
type SaveTemplateRequest struct {
	TemplateID       string         `json:"template_id"`
	Version          int            `json:"version"`
	Title            LocalizedText  `json:"title"`
	Description      LocalizedText  `json:"description"`
	Category         string         `json:"category"`
	BusinessFlow     string         `json:"business_flow"`
	OutputTypes      []string       `json:"output_types"`
	Tags             []string       `json:"tags,omitempty"`
	RequiresReview   bool           `json:"requires_review"`
	SupportsSchedule bool           `json:"supports_schedule"`
	Trust            TrustMetadata  `json:"trust"`
	Compatibility    Compatibility  `json:"compatibility"`
	UISchema         []UIField      `json:"ui_schema"`
	Validation       ValidationRule `json:"validation"`
	Draft            DAGDraft       `json:"draft"`
}
