package prompt

import (
	"context"
	"fmt"
	"strings"
)

var _ DynamicSectionProvider = MCPInstructionsProvider{}

type MCPInstructionsProvider struct{}

func (MCPInstructionsProvider) SectionName() string {
	return DynamicSectionMCPInstructions
}

func (MCPInstructionsProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	snapshot := input.BuildCtx.MCPSnapshot
	if len(snapshot.Servers) == 0 || len(snapshot.Instructions) == 0 {
		return nil, nil
	}

	instructions := normalizedMCPInstructions(snapshot.Instructions)
	servers := make([]string, 0, len(snapshot.Servers))
	for _, server := range sortedPromptValues(snapshot.Servers) {
		if instructions[server] != "" {
			servers = append(servers, server)
		}
	}
	if len(servers) == 0 {
		return nil, nil
	}

	blocks := []string{
		"# MCP Server Instructions",
		"The following MCP servers have provided instructions for how to use their tools and resources:",
	}
	for _, server := range servers {
		blocks = append(blocks, renderMCPServerBlock(server, instructions[server]))
	}

	text := strings.Join(blocks, "\n\n")
	return &text, nil
}

func normalizedMCPInstructions(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for server, instructions := range raw {
		server = strings.TrimSpace(server)
		instructions = strings.TrimSpace(instructions)
		if server == "" || instructions == "" {
			continue
		}
		out[server] = instructions
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func renderMCPServerBlock(server, instructions string) string {
	lines := []string{fmt.Sprintf("## %s", server)}
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		lines = append(lines, instructions)
	}
	return strings.Join(lines, "\n")
}
