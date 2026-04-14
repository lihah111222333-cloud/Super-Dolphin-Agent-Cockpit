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

type EnvInfoProvider struct{}

func (EnvInfoProvider) SectionName() string {
	return DynamicSectionEnvInfoSimple
}

func (EnvInfoProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	lines := []string{
		fmt.Sprintf("- Primary working directory: %s", currentPromptCWD(input.BuildCtx)),
		fmt.Sprintf("- Is a git repository: %s", yesNo(strings.TrimSpace(input.BuildCtx.GitRoot) != "")),
		fmt.Sprintf("- Platform: %s", promptPlatform()),
		fmt.Sprintf("- Shell: %s", promptShellName()),
		fmt.Sprintf("- OS version: %s", promptUnameSR()),
		fmt.Sprintf("- Language server status: %s", promptLanguageServerStatus(input.BuildCtx)),
	}
	if gitRoot := strings.TrimSpace(input.BuildCtx.GitRoot); gitRoot != "" {
		lines = append(lines, fmt.Sprintf("- Git root: %s", gitRoot))
	}
	if input.BuildCtx.IsWorktree {
		lines = append(lines,
			"- Git worktree: yes",
			"- Worktree note: run all commands from this directory and do not cd to the original repository root",
		)
	}
	for _, dir := range sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories) {
		lines = append(lines, fmt.Sprintf("- Additional working directory: %s", dir))
	}
	if provider := strings.TrimSpace(input.BuildCtx.Provider); provider != "" {
		lines = append(lines, fmt.Sprintf("- Provider: %s", provider))
	}
	if metadata := promptModelMetadata(input.BuildCtx); metadata != "" {
		lines = append(lines,
			fmt.Sprintf("- Model metadata: %s", metadata),
			fmt.Sprintf("- Knowledge cutoff: %s", promptKnowledgeCutoff(input.BuildCtx)),
		)
	}
	if guidance := promptFrontierGuidance(input.BuildCtx); guidance != "" {
		lines = append(lines, fmt.Sprintf("- Frontier guidance: %s", guidance))
	}

	text := strings.Join(append([]string{
		"# Environment",
		"You have been invoked in the following environment:",
	}, lines...), "\n")
	return &text, nil
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

var promptUnameSRValue = sync.OnceValue(loadPromptUnameSR)

func promptUnameSR() string {
	return promptUnameSRValue()
}

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
	tools := make([]string, 0, len(build.EnabledTools))
	for _, tool := range sortedPromptValues(build.EnabledTools) {
		if strings.HasPrefix(tool, "lsp_") {
			tools = append(tools, tool)
		}
	}
	return tools
}

func promptModelMetadata(build BuildCtx) string {
	return LookupModelDescriptor(build.Model).MetadataText()
}

func promptKnowledgeCutoff(build BuildCtx) string {
	descriptor := LookupModelDescriptor(build.Model)
	if descriptor.IsZero() {
		return ""
	}
	if cutoff := strings.TrimSpace(descriptor.KnowledgeCutoff); cutoff != "" {
		return cutoff
	}
	return "not published by the provider"
}

func promptFrontierGuidance(build BuildCtx) string {
	return strings.TrimSpace(LookupModelDescriptor(build.Model).FrontierGuidance)
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
