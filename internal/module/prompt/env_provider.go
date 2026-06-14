package prompt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
)

var _ DynamicSectionProvider = EnvInfoProvider{}

const (
	promptWorktreeNote           = "run all commands from this directory and do not cd to the original repository root"
	promptWindowsShellSyntaxNote = "use Unix shell syntax"
)

type EnvInfoProvider struct{}

type promptEnvRenderMode int

const (
	promptEnvRenderSimple promptEnvRenderMode = iota
	promptEnvRenderSubagent
)

type promptEnvSnapshot struct {
	CWD                          string
	GitRoot                      string
	IsGitRepo                    bool
	IsWorktree                   bool
	Platform                     string
	Shell                        string
	ShellNote                    string
	OSVersion                    string
	LanguageServerStatus         string
	AdditionalWorkingDirectories []string
	Provider                     string
	ModelMetadata                string
	KnowledgeCutoff              string
	LatestModelFamily            string
	ProductSurfaces              string
	FastMode                     string
	FrontierGuidance             string
}

var promptEnvXMLEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

// SectionName 处理section名称。
func (EnvInfoProvider) SectionName() string {
	return DynamicSectionEnvInfoSimple
}

// Resolve 解析prompt。
func (EnvInfoProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	snapshot := buildPromptEnvSnapshot(input)
	text := renderSimpleEnvInfo(snapshot)
	if promptEnvRenderModeForInput(input) == promptEnvRenderSubagent {
		text = renderSubagentEnvInfo(snapshot)
	}
	return &text, nil
}

func promptEnvRenderModeForInput(input SectionContext) promptEnvRenderMode {
	if input.Start != nil && input.Turn == nil && strings.TrimSpace(input.Start.ParentAgentID) != "" {
		return promptEnvRenderSubagent
	}
	return promptEnvRenderSimple
}

// String 返回字符串表示。
func (m promptEnvRenderMode) String() string {
	if m == promptEnvRenderSubagent {
		return "subagent"
	}
	return "simple"
}

func buildPromptEnvSnapshot(input SectionContext) promptEnvSnapshot {
	build := input.BuildCtx
	descriptor := LookupModelDescriptor(build.Model)
	return promptEnvSnapshot{
		CWD:                          currentPromptCWD(build),
		GitRoot:                      strings.TrimSpace(build.GitRoot),
		IsGitRepo:                    strings.TrimSpace(build.GitRoot) != "",
		IsWorktree:                   build.IsWorktree,
		Platform:                     promptPlatform(),
		Shell:                        promptShellName(),
		ShellNote:                    promptShellNote(),
		OSVersion:                    promptUnameSR(),
		LanguageServerStatus:         promptLanguageServerStatus(build),
		AdditionalWorkingDirectories: sortedPromptValues(build.AdditionalWorkingDirectories),
		Provider:                     strings.TrimSpace(build.Provider),
		ModelMetadata:                descriptor.MetadataText(),
		KnowledgeCutoff:              descriptor.KnowledgeCutoffText(),
		LatestModelFamily:            descriptor.LatestModelFamilyText(),
		ProductSurfaces:              promptProductSurfaces(),
		FastMode:                     promptFastModeNote(),
		FrontierGuidance:             strings.TrimSpace(descriptor.FrontierGuidance),
	}
}

// renderSimpleEnvInfo 渲染simpleenvinfo。
func renderSimpleEnvInfo(snapshot promptEnvSnapshot) string {
	lines := []string{
		"# Environment",
		"You have been invoked in the following environment:",
		fmt.Sprintf("- Primary working directory: %s", snapshot.CWD),
	}
	if snapshot.IsWorktree {
		lines = append(lines, "- Git worktree: yes", "- Worktree note: "+promptWorktreeNote)
	}
	lines = append(lines,
		fmt.Sprintf("- Is a git repository: %s", yesNo(snapshot.IsGitRepo)),
		fmt.Sprintf("- Platform: %s", snapshot.Platform),
		fmt.Sprintf("- Shell: %s", snapshot.Shell),
	)
	if snapshot.ShellNote != "" {
		lines = append(lines, "- Shell note: "+snapshot.ShellNote)
	}
	lines = append(lines,
		fmt.Sprintf("- OS version: %s", snapshot.OSVersion),
		fmt.Sprintf("- Language server status: %s", snapshot.LanguageServerStatus),
	)
	if snapshot.GitRoot != "" {
		lines = append(lines, fmt.Sprintf("- Git root: %s", snapshot.GitRoot))
	}
	for _, dir := range snapshot.AdditionalWorkingDirectories {
		lines = append(lines, fmt.Sprintf("- Additional working directory: %s", dir))
	}
	if snapshot.Provider != "" {
		lines = append(lines, fmt.Sprintf("- Provider: %s", snapshot.Provider))
	}
	if snapshot.ModelMetadata != "" {
		lines = append(lines,
			fmt.Sprintf("- Model metadata: %s", snapshot.ModelMetadata),
			fmt.Sprintf("- Knowledge cutoff: %s", snapshot.KnowledgeCutoff),
		)
	}
	if snapshot.LatestModelFamily != "" {
		lines = append(lines, fmt.Sprintf("- Latest model family: %s", snapshot.LatestModelFamily))
	}
	lines = append(lines,
		fmt.Sprintf("- Available platforms: %s", snapshot.ProductSurfaces),
		fmt.Sprintf("- Fast mode: %s", snapshot.FastMode),
	)
	if snapshot.FrontierGuidance != "" {
		lines = append(lines, fmt.Sprintf("- Frontier guidance: %s", snapshot.FrontierGuidance))
	}
	return strings.Join(lines, "\n")
}

func renderSubagentEnvInfo(snapshot promptEnvSnapshot) string {
	lines := []string{"Environment details for this subagent are below.", "<env>"}
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("primaryWorkingDirectory", snapshot.CWD))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("isGitRepository", promptBoolText(snapshot.IsGitRepo)))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("gitRoot", snapshot.GitRoot))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("isWorktree", promptBoolText(snapshot.IsWorktree)))
	if snapshot.IsWorktree {
		lines = appendPromptEnvLine(lines, promptEnvXMLLine("worktreeNote", promptWorktreeNote))
	}
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("platform", snapshot.Platform))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("shell", snapshot.Shell))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("shellNote", snapshot.ShellNote))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("osVersion", snapshot.OSVersion))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("languageServerStatus", snapshot.LanguageServerStatus))
	for _, dir := range snapshot.AdditionalWorkingDirectories {
		lines = appendPromptEnvLine(lines, promptEnvXMLLine("additionalWorkingDirectory", dir))
	}
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("provider", snapshot.Provider))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("modelMetadata", snapshot.ModelMetadata))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("knowledgeCutoff", snapshot.KnowledgeCutoff))
	lines = appendPromptEnvLine(lines, promptEnvXMLLine("frontierGuidance", snapshot.FrontierGuidance))
	lines = append(lines, "</env>")
	return strings.Join(lines, "\n")
}

func appendPromptEnvLine(lines []string, line string) []string {
	if strings.TrimSpace(line) == "" {
		return lines
	}
	return append(lines, line)
}

func promptEnvXMLLine(tag, value string) string {
	tag = strings.TrimSpace(tag)
	value = strings.TrimSpace(value)
	if tag == "" || value == "" {
		return ""
	}
	return fmt.Sprintf("  <%s>%s</%s>", tag, promptEnvXMLEscaper.Replace(value), tag)
}

func promptBoolText(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

func currentPromptCWD(build BuildCtx) string {
	if cwd := strings.TrimSpace(build.CWD); cwd != "" {
		return cwd
	}
	cwd, err := os.Getwd()
	if err != nil || strings.TrimSpace(cwd) == "" {
		return "unknown"
	}
	return cwd
}

func promptPlatform() string {
	return runtime.GOOS
}

// promptShellName 处理promptshell名称。
func promptShellName() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		if runtime.GOOS == "windows" {
			return "cmd.exe"
		}
		return "unknown"
	}
	if cut := strings.LastIndexAny(shell, `/\`); cut >= 0 && cut+1 < len(shell) {
		shell = shell[cut+1:]
	}
	shell = strings.TrimSuffix(shell, ".exe")
	if shell == "" {
		return "unknown"
	}
	return shell
}

func promptShellNote() string {
	if runtime.GOOS == "windows" {
		return promptWindowsShellSyntaxNote
	}
	return ""
}

var promptUnameSRValue = sync.OnceValue(loadPromptUnameSR)

func promptUnameSR() string {
	return promptUnameSRValue()
}

// loadPromptUnameSR 加载promptunamesr。
func loadPromptUnameSR() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	output, err := exec.Command("uname", "-sr").Output()
	if err == nil {
		if value := strings.TrimSpace(string(output)); value != "" {
			return value
		}
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

func promptLanguageServerStatus(build BuildCtx) string {
	tools := promptLanguageServerTools(build)
	if len(tools) == 0 {
		return "not enabled in this session"
	}
	return "enabled (" + strings.Join(tools, ", ") + ")"
}

func promptLanguageServerTools(build BuildCtx) []string {
	return canonicalPromptLSPTools(build.EnabledTools)
}

func promptModelMetadata(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).MetadataText()
}

func promptKnowledgeCutoff(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).KnowledgeCutoffText()
}

func promptLatestModelFamily(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).LatestModelFamilyText()
}

func promptFrontierGuidance(build BuildCtx) string {
	return strings.TrimSpace(LookupModelDescriptor(build.Model).FrontierGuidance)
}

func promptProductSurfaces() string {
	return "CLI, desktop app on macOS and Windows, web, and IDE extensions for VS Code and JetBrains"
}

func promptFastModeNote() string {
	return "uses the same model but aims for faster responses; switch with /fast when available"
}

func sortedPromptValues(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
