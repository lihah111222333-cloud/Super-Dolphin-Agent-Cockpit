package turn

import (
	"fmt"
	"strings"
)

func buildFeedbackProposalPrompt(topicKey string, feedbackContents []string) string {
	var sb strings.Builder
	sb.WriteString("你是一个技能提炼专家。以下是用户在多次协作中反复给出的同类反馈：\n\n")
	for i, fb := range feedbackContents {
		fmt.Fprintf(&sb, "反馈 %d:\n%s\n\n", i+1, fb)
	}
	sb.WriteString("请将这些反馈合成为一个 SKILL.md 文件。要求：\n")
	sb.WriteString("1. 以 YAML frontmatter 开头，包含 name 和 description\n")
	sb.WriteString("2. description 以 'Use when' 开头，描述触发条件\n")
	sb.WriteString("3. 正文用 H2 分节，每节一个具体规则\n")
	sb.WriteString("4. 规则要具体可执行，不要泛泛而谈\n")
	sb.WriteString("5. 合并重复内容，保留所有独特要点\n\n")
	sb.WriteString("直接输出 SKILL.md 内容，不要包裹 code fence。")
	return sb.String()
}
