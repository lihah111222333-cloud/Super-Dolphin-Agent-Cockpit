package builtinprompts

import (
	"encoding/json"
	"fmt"
	iofs "io/fs"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// readFileAdapter 把 fs.FS 适配成只暴露 ReadFile 的最小接口。
type readFileAdapter struct {
	fsys iofs.FS
}

// ReadFile 从适配的 fs.FS 中读取内置 prompt 资源。
func (a readFileAdapter) ReadFile(name string) ([]byte, error) {
	return iofs.ReadFile(a.fsys, name)
}

// NewDefaultRegistry 从嵌入资源创建内置 prompt registry。
func NewDefaultRegistry() (contract.BuiltinPromptRegistry, error) {
	sub, err := iofs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, fmt.Errorf("builtin prompts: open embedded assets: %w", err)
	}
	return LoadRegistryFromFS(readFileAdapter{fsys: sub})
}

// LoadRegistryFromFS 从给定文件系统加载 manifest、模板和正文，并在构建 registry 前完成校验。
func LoadRegistryFromFS(source readFileFS) (*Registry, error) {
	rules := newValidationRules()
	manifest, err := loadManifest(source)
	if err != nil {
		return nil, err
	}
	templates := make([]loadedTemplate, 0, len(manifest.Templates))
	for _, path := range manifest.Templates {
		template, err := loadTemplate(source, path, rules)
		if err != nil {
			return nil, err
		}
		templates = append(templates, template)
	}
	if err := validateLoadedTemplates(templates, rules); err != nil {
		return nil, err
	}
	return newRegistry(templates), nil
}

// loadManifest 读取并校验 manifest.json，返回已清理的模板路径列表。
func loadManifest(source readFileFS) (manifestConfig, error) {
	data, err := source.ReadFile("manifest.json")
	if err != nil {
		return manifestConfig{}, fmt.Errorf("builtin prompts: read manifest.json: %w", err)
	}
	var manifest manifestConfig
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifestConfig{}, fmt.Errorf("builtin prompts: parse manifest.json: %w", err)
	}
	normalizeManifest(&manifest)
	if err := validateManifest(manifest); err != nil {
		return manifestConfig{}, err
	}
	return manifest, nil
}

// loadTemplate 读取单个模板配置和其引用的 section 正文。
func loadTemplate(source readFileFS, path string, rules validationRules) (loadedTemplate, error) {
	data, err := source.ReadFile(path)
	if err != nil {
		return loadedTemplate{}, fmt.Errorf("builtin prompts: read %s: %w", path, err)
	}
	var cfg templateConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return loadedTemplate{}, fmt.Errorf("builtin prompts: parse %s: %w", path, err)
	}
	normalizeTemplate(&cfg)
	if err := validateTemplateConfig(path, cfg, rules); err != nil {
		return loadedTemplate{}, err
	}
	sections, err := loadSections(source, path, cfg.Sections)
	if err != nil {
		return loadedTemplate{}, err
	}
	return loadedTemplate{Path: path, Config: cfg, Sections: sections}, nil
}

// loadSections 读取模板声明的正文文件，并清理首尾空白。
func loadSections(source readFileFS, templatePath string, sections []sectionConfig) ([]loadedSection, error) {
	loaded := make([]loadedSection, 0, len(sections))
	for _, section := range sections {
		data, err := source.ReadFile(section.BodyFile)
		if err != nil {
			return nil, fmt.Errorf("builtin prompts: read body_file %s for %s: %w", section.BodyFile, templatePath, err)
		}
		loaded = append(loaded, loadedSection{
			Config: section,
			Body:   strings.TrimSpace(string(data)),
		})
	}
	return loaded, nil
}

// normalizeManifest 清理 manifest 中的模板路径。
func normalizeManifest(manifest *manifestConfig) {
	for i := range manifest.Templates {
		manifest.Templates[i] = strings.TrimSpace(manifest.Templates[i])
	}
}

// normalizeTemplate 清理模板配置字段，并规范可选 JSON 条件。
func normalizeTemplate(cfg *templateConfig) {
	cfg.PromptKey = strings.TrimSpace(cfg.PromptKey)
	cfg.Kind = strings.TrimSpace(cfg.Kind)
	cfg.Title = strings.TrimSpace(cfg.Title)
	cfg.AgentKey = strings.TrimSpace(cfg.AgentKey)
	cfg.ToolName = strings.TrimSpace(cfg.ToolName)
	cfg.PromptText = strings.TrimSpace(cfg.PromptText)
	cfg.WhenToUse = strings.TrimSpace(cfg.WhenToUse)
	cfg.Description = strings.TrimSpace(cfg.Description)
	cfg.Scope = strings.TrimSpace(cfg.Scope)
	cfg.MatchWhen = normalizeRawJSON(cfg.MatchWhen)
	for i := range cfg.Tags {
		cfg.Tags[i] = strings.TrimSpace(cfg.Tags[i])
	}
	for i := range cfg.Sections {
		normalizeSection(&cfg.Sections[i])
	}
}

// normalizeSection 清理 section 配置字段，并规范启用条件 JSON。
func normalizeSection(section *sectionConfig) {
	section.SectionKey = strings.TrimSpace(section.SectionKey)
	section.Region = strings.TrimSpace(section.Region)
	section.BodyFile = strings.TrimSpace(section.BodyFile)
	section.TriggerType = strings.TrimSpace(section.TriggerType)
	section.RecallTopic = strings.TrimSpace(section.RecallTopic)
	section.EnableWhen = normalizeRawJSON(section.EnableWhen)
}

// normalizeRawJSON 把空白或 null JSON 视为未配置，其他内容保持原始 JSON。
func normalizeRawJSON(raw json.RawMessage) json.RawMessage {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	return json.RawMessage(trimmed)
}
