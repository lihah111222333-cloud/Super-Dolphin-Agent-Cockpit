package turn

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

func buildPrepareInput(p turnStartParams, session contract.Session) PrepareInput {
	return PrepareInput{
		Prompt:     p.Prompt,
		Images:     p.Images,
		Files:      p.Files,
		Model:      p.Model,
		Effort:     p.Effort,
		ThreadCaps: session.Capabilities(),
	}
}
