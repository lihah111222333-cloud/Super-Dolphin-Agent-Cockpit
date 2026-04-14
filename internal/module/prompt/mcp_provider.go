package prompt

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

var _ DynamicSectionProvider = MCPInstructionsProvider{}

type MCPInstructionsProvider struct{}

func (MCPInstructionsProvider) SectionName() string {
	return DynamicSectionMCPInstructions
}

func (MCPInstructionsProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	snapshot := input.BuildCtx.MCPSnapshot
	if len(snapshot.Servers) == 0 && len(snapshot.Tools) == 0 && len(snapshot.Instructions) == 0 {
		return nil, nil
	}

	servers := sortedPromptValues(snapshot.Servers)
	groups, looseTools := groupMCPTools(snapshot.Tools)
	instructions := normalizedMCPInstructions(snapshot.Instructions)
	for server := range groups {
		servers = append(servers, server)
	}
	for server := range instructions {
		servers = append(servers, server)
	}
	servers = sortedPromptValues(servers)

	blocks := []string{
		"# MCP Server Instructions",
		"The following MCP servers are currently connected. Prefer their tools and resources when they match the task, and call tools by their exact names.",
	}
	for _, server := range servers {
		blocks = append(blocks, renderMCPServerBlock(server, groups[server], instructions[server]))
	}
	if len(looseTools) > 0 {
		blocks = append(blocks, renderMCPAdditionalToolsBlock(looseTools))
	}

	text := strings.Join(blocks, "\n\n")
	return &text, nil
}

func groupMCPTools(tools []string) (map[string][]string, []string) {
	grouped := make(map[string][]string)
	loose := make([]string, 0)
	for _, tool := range sortedPromptValues(tools) {
		server, ok := parseMCPServerName(tool)
		if !ok {
			loose = append(loose, tool)
			continue
		}
		grouped[server] = append(grouped[server], tool)
	}
	for server := range grouped {
		sort.Strings(grouped[server])
	}
	return grouped, loose
}

func parseMCPServerName(tool string) (string, bool) {
	parts := strings.Split(tool, "__")
	if len(parts) < 3 || strings.TrimSpace(parts[0]) != "mcp" {
		return "", false
	}
	server := strings.TrimSpace(parts[1])
	if server == "" {
		return "", false
	}
	return server, true
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

func renderMCPServerBlock(server string, tools []string, instructions string) string {
	lines := []string{fmt.Sprintf("## %s", server)}
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		lines = append(lines, instructions)
	}
	if len(tools) == 0 {
		if len(lines) == 1 {
			lines = append(lines, "- Connected, but no tool snapshot was provided.")
		}
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "- Exact tool names:")
	for _, tool := range tools {
		lines = append(lines, "  - "+tool)
	}
	return strings.Join(lines, "\n")
}

func renderMCPAdditionalToolsBlock(tools []string) string {
	lines := []string{"## additional_tools", "- Tools without a server mapping in the snapshot:"}
	for _, tool := range tools {
		lines = append(lines, "  - "+tool)
	}
	return strings.Join(lines, "\n")
}
