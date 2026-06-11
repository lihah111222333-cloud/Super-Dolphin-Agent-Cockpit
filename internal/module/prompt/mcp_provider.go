package prompt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const mcpInstructionsDeltaAttachmentKind = "mcp_instructions_delta"

var (
	_ DynamicSectionProvider        = MCPInstructionsProvider{}
	_ dynamicTurnAttachmentProvider = MCPInstructionsProvider{}

	defaultMCPInstructionsTracker = sync.OnceValue(newMCPInstructionsTracker)
)

type MCPInstructionsProvider struct {
	tracker *mcpInstructionsTracker
}

type mcpInstructionsTracker struct {
	mu      sync.Mutex
	threads map[string]map[string]string
}

type mcpInstructionsDiff struct {
	added   map[string]string
	removed []string
}

func newMCPInstructionsTracker() *mcpInstructionsTracker {
	return &mcpInstructionsTracker{threads: map[string]map[string]string{}}
}

func (p MCPInstructionsProvider) SectionName() string {
	return DynamicSectionMCPInstructions
}

func (p MCPInstructionsProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	snapshot := input.BuildCtx.MCPSnapshot
	if snapshot.InstructionsDeltaEnabled {
		return nil, nil
	}
	instructions := liveMCPInstructions(snapshot)
	if len(instructions) == 0 {
		return nil, nil
	}
	blocks := []string{
		"# MCP Server Instructions",
		"The following MCP servers have provided instructions for how to use their tools and resources:",
	}
	for _, server := range sortedMCPInstructionServers(instructions) {
		blocks = append(blocks, renderMCPServerBlock(server, instructions[server]))
	}
	text := strings.Join(blocks, "\n\n")
	return &text, nil
}

func (p MCPInstructionsProvider) ResolveTurnAttachments(_ context.Context, input SectionContext) []dto.AttachmentEnvelope {
	if input.Turn == nil {
		return nil
	}
	threadID := strings.TrimSpace(input.Turn.ThreadID)
	tracker := p.trackerOrDefault()
	if threadID == "" {
		return nil
	}
	snapshot := input.BuildCtx.MCPSnapshot
	if !snapshot.InstructionsDeltaEnabled {
		tracker.Reset(threadID)
		return nil
	}
	diff := tracker.Update(threadID, liveMCPInstructions(snapshot))
	attachment, ok := diff.Attachment()
	if !ok {
		return nil
	}
	return []dto.AttachmentEnvelope{attachment}
}

func (p MCPInstructionsProvider) trackerOrDefault() *mcpInstructionsTracker {
	if p.tracker != nil {
		return p.tracker
	}
	return defaultMCPInstructionsTracker()
}

func (t *mcpInstructionsTracker) Update(threadID string, current map[string]string) mcpInstructionsDiff {
	threadID = strings.TrimSpace(threadID)
	if t == nil || threadID == "" {
		return mcpInstructionsDiff{}
	}
	current = cloneMCPInstructionMap(current)
	t.mu.Lock()
	previous := cloneMCPInstructionMap(t.threads[threadID])
	if len(current) == 0 {
		delete(t.threads, threadID)
	} else {
		t.threads[threadID] = cloneMCPInstructionMap(current)
	}
	t.mu.Unlock()
	return diffMCPInstructions(previous, current)
}

func (t *mcpInstructionsTracker) Reset(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if t == nil || threadID == "" {
		return
	}
	t.mu.Lock()
	delete(t.threads, threadID)
	t.mu.Unlock()
}

func diffMCPInstructions(previous, current map[string]string) mcpInstructionsDiff {
	diff := mcpInstructionsDiff{added: map[string]string{}}
	for server, instructions := range current {
		if strings.TrimSpace(instructions) == "" || previous[server] == instructions {
			continue
		}
		diff.added[server] = instructions
	}
	for server := range previous {
		if _, ok := current[server]; !ok {
			diff.removed = append(diff.removed, server)
		}
	}
	if len(diff.added) == 0 {
		diff.added = nil
	}
	if len(diff.removed) > 1 {
		diff.removed = sortedPromptValues(diff.removed)
	}
	return diff
}

func (d mcpInstructionsDiff) Attachment() (dto.AttachmentEnvelope, bool) {
	if len(d.added) == 0 && len(d.removed) == 0 {
		return dto.AttachmentEnvelope{}, false
	}
	now := time.Now().UTC()
	return dto.AttachmentEnvelope{
		Kind:      mcpInstructionsDeltaAttachmentKind,
		Path:      "/mcp/mcp_instructions_delta.md",
		Header:    "# MCP Server Instructions Delta",
		Content:   renderMCPInstructionsDelta(d),
		MtimeMs:   now.UnixMilli(),
		UpdatedAt: now.Format(time.RFC3339),
	}, true
}

func renderMCPInstructionsDelta(diff mcpInstructionsDiff) string {
	blocks := make([]string, 0, len(diff.added)+len(diff.removed)+2)
	if len(diff.added) > 0 {
		blocks = append(blocks, "Apply these MCP instruction updates immediately:")
		for _, server := range sortedMCPInstructionServers(diff.added) {
			blocks = append(blocks, renderMCPServerBlock(server, diff.added[server]))
		}
	}
	if len(diff.removed) > 0 {
		blocks = append(blocks, "Forget these MCP instruction blocks; they no longer apply:")
		for _, server := range diff.removed {
			blocks = append(blocks, fmt.Sprintf("## %s\nThese MCP server instructions no longer apply and should be ignored.", server))
		}
	}
	return strings.Join(blocks, "\n\n")
}

func liveMCPInstructions(snapshot MCPSnapshot) map[string]string {
	instructions := normalizedMCPInstructions(snapshot.Instructions)
	if len(snapshot.Servers) == 0 || len(instructions) == 0 {
		return nil
	}
	live := make(map[string]string, len(snapshot.Servers))
	for _, server := range sortedPromptValues(snapshot.Servers) {
		if text := instructions[server]; text != "" {
			live[server] = text
		}
	}
	if len(live) == 0 {
		return nil
	}
	return live
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

func sortedMCPInstructionServers(instructions map[string]string) []string {
	servers := make([]string, 0, len(instructions))
	for server := range instructions {
		servers = append(servers, server)
	}
	return sortedPromptValues(servers)
}

func cloneMCPInstructionMap(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(raw))
	for key, value := range raw {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			cloned[key] = value
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}
