package prompt

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
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
		fmt.Sprintf("- Current date: %s", currentPromptDate(input)),
		fmt.Sprintf("- OS: %s", promptOSLabel()),
		fmt.Sprintf("- Shell: %s", promptShellName()),
		fmt.Sprintf("- Language server status: %s", promptLanguageServerStatus(input.BuildCtx)),
	}
	if gitRoot := strings.TrimSpace(input.BuildCtx.GitRoot); gitRoot != "" {
		lines = append(lines, fmt.Sprintf("- Git root: %s", gitRoot))
	}
	for _, dir := range sortedPromptValues(input.BuildCtx.AdditionalWorkingDirectories) {
		lines = append(lines, fmt.Sprintf("- Additional working directory: %s", dir))
	}
	if provider := strings.TrimSpace(input.BuildCtx.Provider); provider != "" {
		lines = append(lines, fmt.Sprintf("- Provider: %s", provider))
	}
	if model := strings.TrimSpace(input.BuildCtx.Model); model != "" {
		lines = append(lines, fmt.Sprintf("- Model: %s", model))
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

func currentPromptDate(input SectionContext) string {
	if input.Turn != nil {
		if currentDate := strings.TrimSpace(input.Turn.CurrentDate); currentDate != "" {
			return currentDate
		}
	}
	return time.Now().Format("2006-01-02")
}

func promptOSLabel() string {
	return runtime.GOOS + "/" + runtime.GOARCH
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

func promptLanguageServerStatus(build BuildCtx) string {
	tools := make([]string, 0, len(build.EnabledTools))
	for _, tool := range sortedPromptValues(build.EnabledTools) {
		if strings.HasPrefix(tool, "lsp_") {
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return "not enabled in this session"
	}
	return "enabled (" + strings.Join(tools, ", ") + ")"
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
