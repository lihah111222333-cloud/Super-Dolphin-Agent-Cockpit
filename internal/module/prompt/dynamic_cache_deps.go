package prompt

import (
	"fmt"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// inputScopedSectionDependency 处理inputscopedsectiondependency。
func inputScopedSectionDependency(section PromptSection, input SectionContext) any {
	switch section.Name {
	case DynamicSectionSessionGuidance:
		return struct {
			Section      string   `json:"section"`
			EnabledTools []string `json:"enabledTools,omitempty"`
			SessionFlags []string `json:"sessionFlags,omitempty"`
		}{
			Section:      section.Name,
			EnabledTools: sortedPromptValues(input.BuildCtx.EnabledTools),
			SessionFlags: trueFlagKeys(input.BuildCtx.SessionFlags),
		}
	case DynamicSectionAvailableExperts:
		return availableExpertsSectionDependency(section, input)
	case DynamicSectionRecallCatalog, DynamicSectionProjectDefaultRules:
		return cwdScopedSectionDependency(section, input)
	case DynamicSectionMemory:
		return memorySectionDependency(section, input)
	case DynamicSectionMemoryContext:
		threadID := ""
		userText := ""
		if input.Turn != nil {
			threadID = strings.TrimSpace(input.Turn.ThreadID)
			userText = strings.TrimSpace(input.Turn.UserText)
		}
		return struct {
			Section  string `json:"section"`
			ThreadID string `json:"threadId,omitempty"`
			UserText string `json:"userText,omitempty"`
		}{Section: section.Name, ThreadID: threadID, UserText: userText}
	case DynamicSectionMemoryEntrypoint:
		return memoryEntrypointSectionDependency(section, input)
	case DynamicSectionEnvInfoSimple:
		return envInfoSimpleSectionDependency(section, input)
	case DynamicSectionLanguage:
		return struct {
			Section  string `json:"section"`
			Language string `json:"language,omitempty"`
		}{Section: section.Name, Language: strings.TrimSpace(input.BuildCtx.Language)}
	default:
		return struct {
			Section string `json:"section"`
		}{Section: section.Name}
	}
}

func availableExpertsSectionDependency(section PromptSection, input SectionContext) any {
	startPrompt := ""
	promptKey := ""
	if input.Start != nil {
		startPrompt = strings.TrimSpace(input.Start.Prompt)
		promptKey = strings.TrimSpace(input.Start.PromptKey)
	}
	turnUserText := ""
	if input.Turn != nil {
		turnUserText = strings.TrimSpace(input.Turn.UserText)
		if promptKey == "" {
			promptKey = strings.TrimSpace(input.Turn.PromptKey)
		}
	}
	return struct {
		Section      string `json:"section"`
		CWD          string `json:"cwd,omitempty"`
		StartPrompt  string `json:"startPrompt,omitempty"`
		TurnUserText string `json:"turnUserText,omitempty"`
		PromptKey    string `json:"promptKey,omitempty"`
	}{
		Section:      section.Name,
		CWD:          contract.SectionContextCWD(input),
		StartPrompt:  startPrompt,
		TurnUserText: turnUserText,
		PromptKey:    promptKey,
	}
}

func cwdScopedSectionDependency(section PromptSection, input SectionContext) any {
	return struct {
		Section string `json:"section"`
		CWD     string `json:"cwd,omitempty"`
	}{
		Section: section.Name,
		CWD:     contract.SectionContextCWD(input),
	}
}

// memorySectionDependency 处理记忆sectiondependency。
func memorySectionDependency(section PromptSection, input SectionContext) any {
	isChild, agentType := childAgentCacheDependency(input)
	return struct {
		Section                      string   `json:"section"`
		CWD                          string   `json:"cwd,omitempty"`
		GitRoot                      string   `json:"gitRoot,omitempty"`
		IsChild                      bool     `json:"isChild,omitempty"`
		AgentType                    string   `json:"agentType,omitempty"`
		AdditionalWorkingDirectories []string `json:"additionalWorkingDirectories,omitempty"`
		SessionFlags                 []string `json:"sessionFlags,omitempty"`
		Harness                      string   `json:"harness,omitempty"`
		DisableAutoMemory            string   `json:"disableAutoMemory,omitempty"`
		DisableClaudeMds             string   `json:"disableClaudeMds,omitempty"`
		SimpleMode                   string   `json:"simpleMode,omitempty"`
		RemoteMode                   string   `json:"remoteMode,omitempty"`
		MemoryRootEnv                string   `json:"memoryRootEnv,omitempty"`
		RemoteMemDirEnv              string   `json:"remoteMemDirEnv,omitempty"`
		MemoryPathOverride           string   `json:"memoryPathOverride,omitempty"`
		CoworkPathOverride           string   `json:"coworkPathOverride,omitempty"`
		TeamMemFeat                  string   `json:"teamMemFeat,omitempty"`
		KairosFeat                   string   `json:"kairosFeat,omitempty"`
		SearchPastContextFeat        string   `json:"searchPastContextFeat,omitempty"`
	}{
		Section:                      section.Name,
		CWD:                          currentPromptCWD(input.BuildCtx),
		GitRoot:                      strings.TrimSpace(input.BuildCtx.GitRoot),
		IsChild:                      isChild,
		AgentType:                    agentType,
		AdditionalWorkingDirectories: sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories),
		SessionFlags:                 sortedPromptFlagPairs(input.BuildCtx.SessionFlags),
		Harness:                      os.Getenv("MULTI_AGENT_HARNESS_CLI"),
		DisableAutoMemory:            os.Getenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY"),
		DisableClaudeMds:             os.Getenv("CLAUDE_CODE_DISABLE_CLAUDE_MDS"),
		SimpleMode:                   os.Getenv("CLAUDE_CODE_SIMPLE"),
		RemoteMode:                   os.Getenv("CLAUDE_CODE_REMOTE"),
		MemoryRootEnv:                os.Getenv("MULTI_AGENT_MEMORY_DIR"),
		RemoteMemDirEnv:              os.Getenv("CLAUDE_CODE_REMOTE_MEMORY_DIR"),
		MemoryPathOverride:           os.Getenv("MULTI_AGENT_MEMORY_PATH_OVERRIDE"),
		CoworkPathOverride:           os.Getenv("CLAUDE_COWORK_MEMORY_PATH_OVERRIDE"),
		TeamMemFeat:                  os.Getenv("MULTI_AGENT_MEMORY_FEATURE_TEAMMEM"),
		KairosFeat:                   os.Getenv("MULTI_AGENT_MEMORY_FEATURE_KAIROS"),
		SearchPastContextFeat:        os.Getenv("MULTI_AGENT_MEMORY_FEATURE_SEARCH_PAST_CONTEXT"),
	}
}

func memoryEntrypointSectionDependency(section PromptSection, input SectionContext) any {
	return memorySectionDependency(section, input)
}

// envInfoSimpleSectionDependency 处理envinfosimplesectiondependency。
func envInfoSimpleSectionDependency(section PromptSection, input SectionContext) any {
	return struct {
		Section                      string   `json:"section"`
		RenderMode                   string   `json:"renderMode,omitempty"`
		CWD                          string   `json:"cwd,omitempty"`
		GitRoot                      string   `json:"gitRoot,omitempty"`
		IsWorktree                   bool     `json:"isWorktree,omitempty"`
		Platform                     string   `json:"platform,omitempty"`
		Shell                        string   `json:"shell,omitempty"`
		ShellNote                    string   `json:"shellNote,omitempty"`
		OSVersion                    string   `json:"osVersion,omitempty"`
		LanguageServerTools          []string `json:"languageServerTools,omitempty"`
		AdditionalWorkingDirectories []string `json:"additionalWorkingDirectories,omitempty"`
		Provider                     string   `json:"provider,omitempty"`
		ModelMetadata                string   `json:"modelMetadata,omitempty"`
		KnowledgeCutoff              string   `json:"knowledgeCutoff,omitempty"`
		LatestModelFamily            string   `json:"latestModelFamily,omitempty"`
		FrontierGuidance             string   `json:"frontierGuidance,omitempty"`
	}{
		Section:                      section.Name,
		RenderMode:                   promptEnvRenderModeForInput(input).String(),
		CWD:                          currentPromptCWD(input.BuildCtx),
		GitRoot:                      strings.TrimSpace(input.BuildCtx.GitRoot),
		IsWorktree:                   input.BuildCtx.IsWorktree,
		Platform:                     promptPlatform(),
		Shell:                        promptShellName(),
		ShellNote:                    promptShellNote(),
		OSVersion:                    promptUnameSR(),
		LanguageServerTools:          sectionLanguageServerTools(input.BuildCtx),
		AdditionalWorkingDirectories: sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories),
		Provider:                     strings.TrimSpace(input.BuildCtx.Provider),
		ModelMetadata:                promptModelMetadata(input.BuildCtx),
		KnowledgeCutoff:              promptKnowledgeCutoff(input.BuildCtx),
		LatestModelFamily:            promptLatestModelFamily(input.BuildCtx),
		FrontierGuidance:             promptFrontierGuidance(input.BuildCtx),
	}
}

// cacheByNameSectionDependency 按名称sectiondependency处理缓存。
func cacheByNameSectionDependency(section PromptSection, input SectionContext) any {
	switch section.Name {
	case DynamicSectionOutputStyle:
		style := input.BuildCtx.OutputStyleConfig
		if style == nil {
			return nil
		}
		return struct {
			Section     string `json:"section"`
			Name        string `json:"name,omitempty"`
			Description string `json:"description,omitempty"`
			Prompt      string `json:"prompt,omitempty"`
			Source      string `json:"source,omitempty"`
		}{Section: section.Name, Name: strings.TrimSpace(style.Name), Description: strings.TrimSpace(style.Description), Prompt: strings.TrimSpace(style.Prompt), Source: strings.TrimSpace(style.Source)}
	case DynamicSectionScratchpad:
		dir := strings.TrimSpace(input.BuildCtx.ScratchpadDir)
		if dir == "" {
			return nil
		}
		return struct {
			Section       string `json:"section"`
			ScratchpadDir string `json:"scratchpadDir,omitempty"`
		}{Section: section.Name, ScratchpadDir: dir}
	case DynamicSectionFRC:
		cfg := input.BuildCtx.FRCConfig.Normalize()
		if cfg == nil {
			return nil
		}
		return struct {
			Section                      string   `json:"section"`
			Model                        string   `json:"model,omitempty"`
			Enabled                      bool     `json:"enabled,omitempty"`
			SystemPromptSuggestSummaries bool     `json:"systemPromptSuggestSummaries,omitempty"`
			KeepRecent                   int      `json:"keepRecent,omitempty"`
			SupportedModels              []string `json:"supportedModels,omitempty"`
		}{
			Section:                      section.Name,
			Model:                        strings.TrimSpace(input.BuildCtx.Model),
			Enabled:                      cfg.Enabled,
			SystemPromptSuggestSummaries: cfg.SystemPromptSuggestSummaries,
			KeepRecent:                   cfg.KeepRecentCount(),
			SupportedModels:              append([]string(nil), cfg.SupportedModels...),
		}
	case DynamicSectionNumericLengthAnchors:
		return struct {
			Section  string `json:"section"`
			UserType string `json:"userType,omitempty"`
		}{Section: section.Name, UserType: strings.TrimSpace(promptUserType())}
	case DynamicSectionTokenBudget:
		return struct {
			Section string `json:"section"`
			Enabled bool   `json:"enabled,omitempty"`
		}{Section: section.Name, Enabled: tokenBudgetEnabled(input.BuildCtx)}
	case DynamicSectionBrief:
		return struct {
			Section string `json:"section"`
			Enabled bool   `json:"enabled,omitempty"`
			Summary string `json:"summary,omitempty"`
		}{Section: section.Name, Enabled: briefEnabled(input.BuildCtx), Summary: strings.TrimSpace(input.BuildCtx.Summary)}
	default:
		return nil
	}
}

// childAgentCacheDependency 处理child代理缓存dependency。
func childAgentCacheDependency(input SectionContext) (bool, string) {
	if input.Start == nil || input.Turn != nil || strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return false, ""
	}
	agentType := strings.TrimSpace(input.Start.AgentType)
	if agentType == "" {
		agentType = strings.TrimSpace(input.Start.Name)
	}
	if agentType == "" {
		return false, ""
	}
	return true, agentType
}

func trueFlagKeys(flags map[string]bool) []string {
	keys := make([]string, 0, len(flags))
	for key, enabled := range flags {
		if enabled {
			keys = append(keys, key)
		}
	}
	return sortedPromptValues(keys)
}

func sortedPromptFlagPairs(flags map[string]bool) []string {
	pairs := make([]string, 0, len(flags))
	for key, enabled := range flags {
		pairs = append(pairs, fmt.Sprintf("%s=%t", strings.TrimSpace(key), enabled))
	}
	return sortedPromptValues(pairs)
}

func sectionLanguageServerTools(build BuildCtx) []string {
	return canonicalPromptLSPTools(build.EnabledTools)
}
