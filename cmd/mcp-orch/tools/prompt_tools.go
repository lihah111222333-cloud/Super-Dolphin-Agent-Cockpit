package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const promptSectionsPreviewMaxRunes = 200

type promptListInput struct {
	Keyword  string `json:"keyword,omitempty"`
	Envelope bool   `json:"envelope,omitempty"`
}

type promptGetInput struct {
	PromptKey string `json:"prompt_key"`
	Pos       string `json:"pos,omitempty"`
}

type promptTemplateDTO struct {
	ID             int64           `json:"id"`
	PromptKey      string          `json:"prompt_key"`
	Title          string          `json:"title"`
	AgentKey       string          `json:"agent_key"`
	ToolName       string          `json:"tool_name"`
	PromptText     string          `json:"prompt_text"`
	Variables      json.RawMessage `json:"variables,omitempty"`
	Tags           json.RawMessage `json:"tags,omitempty"`
	Enabled        bool            `json:"enabled"`
	ManuallyEdited bool            `json:"manually_edited"`
	CreatedBy      string          `json:"created_by"`
	UpdatedBy      string          `json:"updated_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Description    string          `json:"description"`
}

type PromptListOutput struct {
	Prompts   []promptTemplateDTO `json:"prompts"`
	Data      []promptTemplateDTO `json:"data"`
	Total     int                 `json:"total"`
	Showing   int                 `json:"showing"`
	Truncated bool                `json:"truncated"`
	Hint      string              `json:"hint,omitempty"`
}

// HandlePromptList 处理promptlist。
func HandlePromptList(store promptstore.Store) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptListInput) (any, error) {
		templates, err := listPromptTemplates(ctx, store, in)
		if err != nil {
			return nil, err
		}
		if in.Envelope {
			return newPromptListOutput(templates), nil
		}
		return templates, nil
	})
}

// HandlePromptGet 处理promptget。
func HandlePromptGet(store promptstore.Store) ToolHandler {
	return makeHandler(store, "prompt store", func(ctx context.Context, in promptGetInput) (promptTemplateDTO, error) {
		return getPromptTemplate(ctx, store, in)
	})
}

func promptToolDefinitions(store promptstore.Store) []ToolDefinition {
	return resourceToolDefinitions(resourceToolSpec{
		ListName:        "prompt_list",
		ListDescription: "List available prompt templates. Templates can be used to generate structured prompts for agents.",
		GetName:         "prompt_get",
		GetDescription:  "Get a specific prompt template by its key, including the prompt text and variables.",
		KeyField:        "prompt_key",
		KeyDescription:  "Prompt template key.",
		ListHandler:     HandlePromptList(store),
		GetHandler:      HandlePromptGet(store),
	})
}

func listPromptTemplates(ctx context.Context, store promptstore.Store, input promptListInput) ([]promptTemplateDTO, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return nil, err
	}
	cwd, err := promptToolTrustedCWD(ctx)
	if err != nil {
		return nil, err
	}
	templates, err := store.List(ctx, promptstore.ListFilter{
		AgentKey:       "",
		Keyword:        strings.TrimSpace(input.Keyword),
		CWD:            cwd,
		RuntimeVisible: true,
		Limit:          resourceListLimit,
	})
	if err != nil {
		return nil, err
	}
	return mapPromptTemplates(templates), nil
}

func newPromptListOutput(templates []promptTemplateDTO) PromptListOutput {
	env := newListEnvelope(templates, int(resourceListLimit), "next: use prompt_get pos=prompt:<prompt_key> for full prompt")
	return PromptListOutput{
		Prompts:   templates,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// getPromptTemplate 读取prompttemplate。
func getPromptTemplate(ctx context.Context, store promptstore.Store, input promptGetInput) (promptTemplateDTO, error) {
	if err := requireDependency(store, "prompt store"); err != nil {
		return promptTemplateDTO{}, err
	}
	cwd, err := promptToolTrustedCWD(ctx)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	promptKey, err := resolvePromptKeyInput(input.PromptKey, input.Pos)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	template, err := store.Get(ctx, promptKey)
	template, err = loadOrNotFound(template, err, "prompt", promptKey)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	if !promptTemplateRuntimeVisible(*template, cwd) {
		return promptTemplateDTO{}, fmt.Errorf("prompt %s not found", promptKey)
	}
	dto := promptTemplateFromStore(*template)
	sections, err := store.ListSectionsByTemplateID(ctx, template.ID)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	if preview := promptSectionsPreview(sections); preview != "" {
		dto.PromptText = preview
	}
	return dto, nil
}

func promptToolTrustedCWD(ctx context.Context) (string, error) {
	scope, ok := common.ToolScopeFromContext(ctx)
	if !ok || strings.TrimSpace(scope.CWD) == "" {
		return "", fmt.Errorf("prompt tools require trusted cwd")
	}
	return scope.CWD, nil
}

func promptTemplateRuntimeVisible(template promptstore.PromptTemplate, cwd string) bool {
	if !template.Enabled {
		return false
	}
	return promptTagsContainScope(template.Tags, strings.TrimSpace(cwd))
}

// promptTagsContainScope 判断 prompt tags 是否包含指定 scope。
func promptTagsContainScope(raw json.RawMessage, cwd string) bool {
	if cwd == "" {
		return false
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return false
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "scope.global" || tag == "scope.cwd:"+cwd {
			return true
		}
	}
	return false
}

func mapPromptTemplates(templates []promptstore.PromptTemplate) []promptTemplateDTO {
	mapped := make([]promptTemplateDTO, 0, len(templates))
	for _, template := range templates {
		mapped = append(mapped, promptTemplateFromStore(template))
	}
	return mapped
}

func promptTemplateFromStore(template promptstore.PromptTemplate) promptTemplateDTO {
	return promptTemplateDTO{
		ID:             template.ID,
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Variables:      shared.CloneRawMessage(template.Variables),
		Tags:           shared.CloneRawMessage(template.Tags),
		Enabled:        template.Enabled,
		ManuallyEdited: template.ManuallyEdited,
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
		CreatedAt:      template.CreatedAt,
		UpdatedAt:      template.UpdatedAt,
		Description:    template.Description,
	}
}

// promptSectionsPreview 处理promptsectionspreview。
func promptSectionsPreview(sections []promptstore.PromptTemplateSection) string {
	sorted := make([]promptstore.PromptTemplateSection, len(sections))
	copy(sorted, sections)
	sort.SliceStable(sorted, func(i, j int) bool { return promptSectionPreviewLess(sorted[i], sorted[j]) })
	blocks := make([]string, 0, len(sorted))
	for _, section := range sorted {
		if !section.Enabled {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
			continue
		}
		if body := strings.TrimSpace(section.Body); body != "" {
			blocks = append(blocks, body)
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return truncatePromptSectionsPreview(strings.Join(blocks, "\n\n"))
}

func promptSectionPreviewLess(left, right promptstore.PromptTemplateSection) bool {
	if promptSectionRegionPriority(left.Region) != promptSectionRegionPriority(right.Region) {
		return promptSectionRegionPriority(left.Region) < promptSectionRegionPriority(right.Region)
	}
	if left.Ordinal != right.Ordinal {
		return left.Ordinal < right.Ordinal
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	return left.SectionKey < right.SectionKey
}

func promptSectionRegionPriority(region string) int {
	if strings.EqualFold(strings.TrimSpace(region), "static") {
		return 0
	}
	return 1
}

func truncatePromptSectionsPreview(text string) string {
	runes := []rune(text)
	if len(runes) <= promptSectionsPreviewMaxRunes {
		return text
	}
	return string(runes[:promptSectionsPreviewMaxRunes])
}
