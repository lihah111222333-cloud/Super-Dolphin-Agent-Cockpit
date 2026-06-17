package workflowtemplates

// LocalizedText 保存模板入口、目录和表单字段的多语言展示文案。
type LocalizedText struct {
	Zh string `json:"zh" yaml:"zh"`
	En string `json:"en,omitempty" yaml:"en,omitempty"`
}

// UIOption 描述 select/multi_select 字段的一个可选项。
type UIOption struct {
	Value string        `json:"value" yaml:"value"`
	Label LocalizedText `json:"label" yaml:"label"`
}

// UIField 描述模板参数表单字段，前端据此渲染输入控件。
type UIField struct {
	Key         string        `json:"key" yaml:"key"`
	Type        string        `json:"type" yaml:"type"`
	Required    bool          `json:"required" yaml:"required"`
	Label       LocalizedText `json:"label" yaml:"label"`
	Placeholder LocalizedText `json:"placeholder" yaml:"placeholder"`
	Help        LocalizedText `json:"help" yaml:"help"`
	Options     []UIOption    `json:"options,omitempty" yaml:"options"`
}

// Template 是仓库内置政企工作流模板的完整定义。
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
	UISchema         []UIField      `json:"ui_schema" yaml:"ui_schema"`
	DAGTemplate      DAGTemplate    `json:"dag_template" yaml:"dag_template"`
	Validation       ValidationRule `json:"validation" yaml:"validation"`
	FinalOutput      FinalOutput    `json:"final_output" yaml:"final_output"`
}

// TemplateSummary 是列表页和 DAG Designer list 工具使用的轻量模板信息。
type TemplateSummary struct {
	ID               string        `json:"id"`
	Version          int           `json:"version"`
	Title            LocalizedText `json:"title"`
	Description      LocalizedText `json:"description"`
	Category         string        `json:"category"`
	BusinessFlow     string        `json:"business_flow"`
	OutputTypes      []string      `json:"output_types"`
	Tags             []string      `json:"tags"`
	EstimatedNodes   int           `json:"estimated_nodes"`
	RequiresReview   bool          `json:"requires_review"`
	SupportsSchedule bool          `json:"supports_schedule"`
	FinalNodeKey     string        `json:"final_node_key"`
}

// ListFilter 描述模板目录的只读筛选条件。
type ListFilter struct {
	Category         string
	BusinessFlow     string
	OutputType       string
	SupportsSchedule *bool
}

// DAGTemplate 描述模板渲染后的 DAG 草案结构。
type DAGTemplate struct {
	DAGKeyTemplate      string         `json:"dag_key_template" yaml:"dag_key_template"`
	TitleTemplate       string         `json:"title_template" yaml:"title_template"`
	DescriptionTemplate string         `json:"description_template" yaml:"description_template"`
	Trigger             string         `json:"trigger" yaml:"trigger"`
	FinalNodeKey        string         `json:"final_node_key" yaml:"final_node_key"`
	Nodes               []NodeTemplate `json:"nodes" yaml:"nodes"`
}

// NodeTemplate 是模板节点定义；config 保持开放对象以贴合 DAG schema。
type NodeTemplate struct {
	NodeKey    string         `json:"node_key" yaml:"node_key"`
	Title      string         `json:"title" yaml:"title"`
	NodeType   string         `json:"node_type" yaml:"node_type"`
	AssignedTo string         `json:"assigned_to" yaml:"assigned_to"`
	DependsOn  []string       `json:"depends_on" yaml:"depends_on"`
	Config     map[string]any `json:"config" yaml:"config"`
}

// ValidationRule 保存模板级 fail-fast 约束。
type ValidationRule struct {
	SharedFilePrefix         string   `json:"sharedfile_prefix,omitempty" yaml:"sharedfile_prefix"`
	SharedFilePrefixes       []string `json:"sharedfile_prefixes,omitempty" yaml:"sharedfile_prefixes"`
	RequireReviewBeforeFinal bool     `json:"require_review_before_final" yaml:"require_review_before_final"`
	RequireFinalNodeKey      bool     `json:"require_final_node_key" yaml:"require_final_node_key"`
}

// FinalOutput 描述最终交付节点和输出路径。
type FinalOutput struct {
	NodeKey      string `json:"node_key" yaml:"node_key"`
	Kind         string `json:"kind" yaml:"kind"`
	PathTemplate string `json:"path_template" yaml:"path_template"`
}

// RenderRequest 把模板和用户参数渲染成 DAG 草案。
type RenderRequest struct {
	TemplateID     string         `json:"template_id"`
	Version        any            `json:"version,omitempty"`
	Values         map[string]any `json:"values,omitempty"`
	UserInputs     map[string]any `json:"user_inputs,omitempty"`
	RuntimeContext map[string]any `json:"runtime_context,omitempty"`
	TemplateLocale string         `json:"template_locale,omitempty"`
}

// DAGDraft 是模板渲染后的可预览草案，不负责写入 DAG 存储。
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
