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

// EnvInfoProvider 渲染当前工作区、运行平台、模型和工具状态，供动态 system prompt 注入。
// XML 转义器和 uname 缓存由创建它的 prompt Service 独占，避免跨 Service 共享运行时状态。
type EnvInfoProvider struct {
	xmlEscaper *strings.Replacer
	unameSR    func() string
}

// newEnvInfoProvider 创建带独占运行时状态的环境信息 provider。
func newEnvInfoProvider() EnvInfoProvider {
	return EnvInfoProvider{
		xmlEscaper: strings.NewReplacer(
			"&", "&amp;",
			"<", "&lt;",
			">", "&gt;",
			`"`, "&quot;",
			"'", "&apos;",
		),
		unameSR: sync.OnceValue(loadPromptUnameSR),
	}
}

// promptEnvRenderMode 区分主会话的 Markdown 环境提示和子代理的 XML 环境提示。
type promptEnvRenderMode int

const (
	promptEnvRenderSimple promptEnvRenderMode = iota
	promptEnvRenderSubagent
)

// promptEnvSnapshot 是环境提示的只读快照，避免渲染函数直接读取 BuildCtx 和进程环境导致口径分散。
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

// SectionName 返回环境信息动态 section 的注册名。
func (EnvInfoProvider) SectionName() string {
	return DynamicSectionEnvInfoSimple
}

// Resolve 根据调用阶段渲染主会话或子代理环境提示；无外部 IO 失败路径。
func (p EnvInfoProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if p.xmlEscaper == nil || p.unameSR == nil {
		return nil, fmt.Errorf("env info provider is not initialized")
	}
	snapshot := p.buildPromptEnvSnapshot(input)
	text := renderSimpleEnvInfo(snapshot)
	if promptEnvRenderModeForInput(input) == promptEnvRenderSubagent {
		text = p.renderSubagentEnvInfo(snapshot)
	}
	return &text, nil
}

// promptEnvRenderModeForInput 只在 start 阶段且带 parent agent 时切到子代理格式。
func promptEnvRenderModeForInput(input SectionContext) promptEnvRenderMode {
	if input.Start != nil && input.Turn == nil && strings.TrimSpace(input.Start.ParentAgentID) != "" {
		return promptEnvRenderSubagent
	}
	return promptEnvRenderSimple
}

// String 返回渲染模式的缓存键文本。
func (m promptEnvRenderMode) String() string {
	if m == promptEnvRenderSubagent {
		return "subagent"
	}
	return "simple"
}

// buildPromptEnvSnapshot 集中读取 BuildCtx、模型目录和本机环境，保证 Markdown/XML 两种渲染共用同一份数据。
func (p EnvInfoProvider) buildPromptEnvSnapshot(input SectionContext) promptEnvSnapshot {
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
		OSVersion:                    p.unameSR(),
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

// renderSimpleEnvInfo 渲染主会话可读的 Markdown 环境信息。
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

// renderSubagentEnvInfo 渲染子代理可解析的 XML 环境块。
// 空值字段会被跳过，避免把缺失信息写成空标签误导下游代理。
func (p EnvInfoProvider) renderSubagentEnvInfo(snapshot promptEnvSnapshot) string {
	lines := []string{"Environment details for this subagent are below.", "<env>"}
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("primaryWorkingDirectory", snapshot.CWD))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("isGitRepository", promptBoolText(snapshot.IsGitRepo)))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("gitRoot", snapshot.GitRoot))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("isWorktree", promptBoolText(snapshot.IsWorktree)))
	if snapshot.IsWorktree {
		lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("worktreeNote", promptWorktreeNote))
	}
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("platform", snapshot.Platform))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("shell", snapshot.Shell))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("shellNote", snapshot.ShellNote))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("osVersion", snapshot.OSVersion))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("languageServerStatus", snapshot.LanguageServerStatus))
	for _, dir := range snapshot.AdditionalWorkingDirectories {
		lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("additionalWorkingDirectory", dir))
	}
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("provider", snapshot.Provider))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("modelMetadata", snapshot.ModelMetadata))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("knowledgeCutoff", snapshot.KnowledgeCutoff))
	lines = appendPromptEnvLine(lines, p.promptEnvXMLLine("frontierGuidance", snapshot.FrontierGuidance))
	lines = append(lines, "</env>")
	return strings.Join(lines, "\n")
}

// appendPromptEnvLine 只追加非空 XML 行，保持子代理环境块紧凑。
func appendPromptEnvLine(lines []string, line string) []string {
	if strings.TrimSpace(line) == "" {
		return lines
	}
	return append(lines, line)
}

// promptEnvXMLLine 构造单行 XML 字段，并统一处理空 tag、空值和转义。
func (p EnvInfoProvider) promptEnvXMLLine(tag, value string) string {
	tag = strings.TrimSpace(tag)
	value = strings.TrimSpace(value)
	if tag == "" || value == "" {
		return ""
	}
	return fmt.Sprintf("  <%s>%s</%s>", tag, p.xmlEscaper.Replace(value), tag)
}

// promptBoolText 使用小写布尔文本，匹配环境 XML 的稳定格式。
func promptBoolText(ok bool) string {
	if ok {
		return "true"
	}
	return "false"
}

// currentPromptCWD 优先使用 BuildCtx.CWD，缺失时读取进程 cwd 作为环境提示兜底。
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

// promptPlatform 返回 Go runtime 识别的平台名，用于提示里的平台分支。
func promptPlatform() string {
	return runtime.GOOS
}

// promptShellName 返回当前 shell 的短名称；Windows 无 SHELL 时显式标记 cmd.exe。
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

// promptShellNote 返回平台特定 shell 语法提示，非 Windows 会话不附加额外说明。
func promptShellNote() string {
	if runtime.GOOS == "windows" {
		return promptWindowsShellSyntaxNote
	}
	return ""
}

// loadPromptUnameSR 通过 uname -sr 探测类 Unix 版本，失败时退回 GOOS/GOARCH。
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

// promptLanguageServerStatus 汇总当前会话可见的 LSP 工具，空列表明确说明未启用。
func promptLanguageServerStatus(build BuildCtx) string {
	tools := promptLanguageServerTools(build)
	if len(tools) == 0 {
		return "not enabled in this session"
	}
	return "enabled (" + strings.Join(tools, ", ") + ")"
}

// promptLanguageServerTools 返回用于环境提示的规范化 LSP 工具列表。
func promptLanguageServerTools(build BuildCtx) []string {
	return canonicalPromptLSPTools(build.EnabledTools)
}

// promptModelMetadata 返回当前模型的面向用户元数据文本。
func promptModelMetadata(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).MetadataText()
}

// promptKnowledgeCutoff 返回当前模型知识截止信息。
func promptKnowledgeCutoff(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).KnowledgeCutoffText()
}

// promptLatestModelFamily 返回当前模型所属的最新模型族提示。
func promptLatestModelFamily(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).LatestModelFamilyText()
}

// promptFrontierGuidance 返回前沿模型使用建议，缺失时返回空串。
func promptFrontierGuidance(build BuildCtx) string {
	return strings.TrimSpace(LookupModelDescriptor(build.Model).FrontierGuidance)
}

// promptProductSurfaces 返回产品可用入口的固定展示文本。
func promptProductSurfaces() string {
	return "CLI, desktop app on macOS and Windows, web, and IDE extensions for VS Code and JetBrains"
}

// promptFastModeNote 返回 fast mode 的固定说明，避免动态 prompt 误解为切换模型。
func promptFastModeNote() string {
	return "uses the same model but aims for faster responses; switch with /fast when available"
}

// sortedPromptValues 去重、trim 并排序字符串切片，用于稳定环境提示和缓存键。
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

// yesNo 把布尔值渲染为环境 Markdown 中的 yes/no。
func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
