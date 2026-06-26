package builtinprompts

import "encoding/json"

const (
	// 内置 prompt 使用负 ID，避免与数据库中用户创建的正 ID 冲突。
	firstBuiltinID        int64 = -100000
	firstBuiltinSectionID int64 = -200000
)

// readFileFS 是加载器需要的最小文件系统接口，便于 embed.FS 和测试 fixture 共用同一路径校验。
type readFileFS interface {
	ReadFile(name string) ([]byte, error)
}

// manifestConfig 对应内置 prompt manifest.json，只允许声明后续要加载的模板配置路径。
type manifestConfig struct {
	Version   int      `json:"version"`
	Templates []string `json:"templates"`
}

// templateConfig 对应单个内置 prompt JSON 配置，加载后会转换成 store 层可持久化字段。
type templateConfig struct {
	ID          *int64          `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	WhenToUse   string          `json:"when_to_use"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags"`
	Enabled     *bool           `json:"enabled"`
	Scope       string          `json:"scope"`
	MatchWhen   json.RawMessage `json:"match_when"`
	Priority    int             `json:"priority"`
	Sections    []sectionConfig `json:"sections"`
}

// sectionConfig 对应 prompt section JSON 配置，正文必须从 body_file 指向的受控资源读取。
type sectionConfig struct {
	SectionKey  string          `json:"section_key"`
	Region      string          `json:"region"`
	Ordinal     int             `json:"ordinal"`
	BodyFile    string          `json:"body_file"`
	EnableWhen  json.RawMessage `json:"enable_when"`
	Enabled     *bool           `json:"enabled"`
	TriggerType string          `json:"trigger_type"`
	RecallTopic string          `json:"recall_topic"`
}

// loadedTemplate 是加载流程的中间态，保留来源路径便于校验错误指回具体模板文件。
type loadedTemplate struct {
	Path     string
	Config   templateConfig
	Sections []loadedSection
}

// loadedSection 是已展开正文的 section 中间态，进入 registry 前还要做身份声明校验。
type loadedSection struct {
	Config sectionConfig
	Body   string
}
