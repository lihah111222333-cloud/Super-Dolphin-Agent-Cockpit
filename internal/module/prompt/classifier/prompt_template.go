package classifier

import (
	"fmt"
	"strings"
)

// buildClassifierPrompt renders the instruction + candidate list the LLM
// receives. The shape is deliberately strict: one JSON line, no prose, empty
// prompt_key when uncertain. That lets us stay tolerant to model drift
// without re-prompting.
//
// The prompt is kept short on purpose — longer prompts slow haiku down,
// and the classifier already pays a subprocess fork on every call.
func buildClassifierPrompt(in Input) string {
	var sb strings.Builder
	sb.WriteString(`You pick the best prompt_template for a new conversation.

Rules:
- Respond with ONE line of JSON and NOTHING else: {"prompt_key":"<key or empty string>","reason":"<<= 15 words>"}
- Pick the prompt whose scope most clearly matches the user's first message.
- If no candidate is a strong match, return {"prompt_key":"","reason":"no strong match"}.
- Do not wrap the JSON in code fences or add explanations.

User message:
"""
`)
	sb.WriteString(strings.TrimSpace(in.UserInput))
	sb.WriteString("\n\"\"\"\n\nCandidates:\n")
	for i, c := range in.Candidates {
		fmt.Fprintf(&sb, "%d. prompt_key=%s\n", i+1, c.PromptKey)
		if strings.TrimSpace(c.Title) != "" {
			fmt.Fprintf(&sb, "   title: %s\n", strings.TrimSpace(c.Title))
		}
		if strings.TrimSpace(c.Description) != "" {
			fmt.Fprintf(&sb, "   description: %s\n", strings.TrimSpace(c.Description))
		}
		if tags := joinTags(c.Tags); tags != "" {
			fmt.Fprintf(&sb, "   triggers: %s\n", tags)
		}
	}
	sb.WriteString("\nYour JSON:\n")
	return sb.String()
}

func joinTags(tags []string) string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, ", ")
}
