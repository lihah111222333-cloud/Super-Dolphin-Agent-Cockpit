package prompt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

const mcpInstructionsDeltaAttachmentKind = "mcp_instructions_delta"

var (
	_ DynamicSectionProvider        = MCPInstructionsProvider{}
	_ dynamicTurnAttachmentProvider = MCPInstructionsProvider{}

	defaultMCPInstructionsTracker = sync.OnceValue(newMCPInstructionsTracker)
)

// MCPInstructionsProvider 注入或增量推送当前 live MCP server instructions。
// 记录器可由测试注入；生产路径默认按进程记录每个 thread 的上次快照。
type MCPInstructionsProvider struct {
	tracker *mcpInstructionsTracker
}

// mcpInstructionsTracker 保存每个 thread 已发送的 MCP instructions，用于计算后续 turn 的增量。
type mcpInstructionsTracker struct {
	mu      sync.Mutex
	threads map[string]map[string]string
}

// mcpInstructionsDiff 表示一次 turn 中新增/更新和移除的 server instructions。
type mcpInstructionsDiff struct {
	added   map[string]string
	removed []string
}

// newMCPInstructionsTracker 创建空的 thread instructions 记录器。
func newMCPInstructionsTracker() *mcpInstructionsTracker {
	return &mcpInstructionsTracker{threads: map[string]map[string]string{}}
}

// SectionName 返回 MCP instructions 动态 section 的注册名。
func (p MCPInstructionsProvider) SectionName() string {
	return DynamicSectionMCPInstructions
}

// Resolve 在未启用增量附件时渲染完整 MCP instructions section。
// 启用增量模式后这里返回 nil，避免同一内容同时出现在 system prompt 和 turn attachment 中。
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

// ResolveTurnAttachments 在增量模式下生成当前 turn 需要附加的 MCP instructions delta。
// 缺少 threadID 时直接跳过，防止不同会话共享同一记录器状态。
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

// trackerOrDefault 返回注入记录器或进程级默认记录器。
func (p MCPInstructionsProvider) trackerOrDefault() *mcpInstructionsTracker {
	if p.tracker != nil {
		return p.tracker
	}
	return defaultMCPInstructionsTracker()
}

// Update 保存指定 thread 的当前 instructions 快照并返回相对上次快照的 diff。
// 输入 map 会被 clone，调用方后续修改 snapshot 不会影响记录器内部状态。
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

// Reset 清除指定 thread 的 instructions 快照，通常用于从增量模式切回完整 section。
func (t *mcpInstructionsTracker) Reset(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if t == nil || threadID == "" {
		return
	}
	t.mu.Lock()
	delete(t.threads, threadID)
	t.mu.Unlock()
}

// diffMCPInstructions 比较前后两个规范化快照，筛出新增、更新和删除的 server instructions。
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

// Attachment 将非空 diff 渲染为 turn attachment；空 diff 返回 false。
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

// renderMCPInstructionsDelta 渲染 instruct/forget 两类增量说明，供模型立即更新上下文。
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

// liveMCPInstructions 只保留当前 snapshot 中仍然在线的 server instructions。
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

// normalizedMCPInstructions 清理空 server 名和空 instructions，返回可稳定比较的 map。
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

// renderMCPServerBlock 渲染单个 server instructions 的 Markdown 块。
func renderMCPServerBlock(server, instructions string) string {
	lines := []string{fmt.Sprintf("## %s", server)}
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		lines = append(lines, instructions)
	}
	return strings.Join(lines, "\n")
}

// sortedMCPInstructionServers 返回按名称排序的 server 列表，保证 prompt 输出稳定。
func sortedMCPInstructionServers(instructions map[string]string) []string {
	servers := make([]string, 0, len(instructions))
	for server := range instructions {
		servers = append(servers, server)
	}
	return sortedPromptValues(servers)
}

// cloneMCPInstructionMap 复制并清理 instructions map，避免记录器持有调用方可变引用。
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
