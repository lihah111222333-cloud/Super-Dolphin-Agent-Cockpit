package prompt

import (
	"fmt"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type MCPSnapshot = contract.MCPSnapshot

type MCPAttachmentRef = contract.MCPAttachmentRef

type OutputStyleConfig = contract.OutputStyleConfig

type BuildCtx = contract.BuildCtx

const runtimeExtrasRelevanceDisclaimer = "Only use the following runtime extras when they are directly relevant to the user's current request."

func buildTurnUserContext(input TurnInput, resolved []ResolvedPromptSection) string {
	currentDateValue := strings.TrimSpace(input.CurrentDate)
	if currentDateValue == "" {
		currentDateValue = time.Now().Format("2006-01-02")
	}
	currentDate := fmt.Sprintf("Today's date is %s.", currentDateValue)
	return formatUserContextMessage(nil, []string{currentDate}, collectRuntimeUserContext(resolved))
}

func collectRuntimeUserContext(resolved []ResolvedPromptSection) []string {
	rendered := strings.TrimSpace(renderResolvedSections(resolved))
	if rendered == "" {
		return nil
	}
	return []string{rendered}
}

func formatUserContextMessage(claudeMd, currentDate, runtimeExtras []string) string {
	blocks := []string{"<system-reminder>"}
	if block := userContextBlock("claudeMd", claudeMd); block != "" {
		blocks = append(blocks, block)
	}
	if block := userContextBlock("currentDate", currentDate); block != "" {
		blocks = append(blocks, block)
	}
	runtimeLines := append([]string{runtimeExtrasRelevanceDisclaimer}, runtimeExtras...)
	if block := userContextBlock("runtimeExtras", runtimeLines); block != "" {
		blocks = append(blocks, block)
	}
	blocks = append(blocks, "</system-reminder>")
	return strings.Join(blocks, "\n\n")
}

func userContextBlock(key string, lines []string) string {
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			cleaned = append(cleaned, line)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return "# " + strings.TrimSpace(key) + "\n" + strings.Join(cleaned, "\n")
}
