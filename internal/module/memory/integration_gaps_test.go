//go:build e2e
// +build e2e

package memory

import (
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

// Phase 1.6 removed nested ClaudeMd injection of AutoMem / TeamMem MEMORY.md.
// MemoryEntrypointProvider (in the parent memory package) is now the sole
// prompt-time injector. Two e2e cases that asserted the old turn-time team
// injection (`TestRegisterPromptProvidersInjectsTeamMemoryIntoTurnUserContext`
// and `TestRegisterPromptProvidersSkipsTeamMemoryTurnLaneWhenKairosActive`)
// were dropped in Phase 1.7 since they tested removed behaviour. Equivalent
// coverage for entrypoint injection lives in
// `internal/module/memory/entrypoint_provider_test.go`.

func findResolvedSection(sections []prompt.ResolvedPromptSection, name string) (prompt.ResolvedPromptSection, bool) {
	for _, section := range sections {
		if section.Name == name {
			return section, true
		}
	}
	return prompt.ResolvedPromptSection{}, false
}
