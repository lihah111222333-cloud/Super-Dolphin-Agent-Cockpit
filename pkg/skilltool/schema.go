// Package skilltool 暴露 skill_expand_body / skill_read_resource 这两个工具的
// 公共定义：工具名常量 + InputSchema（JSON Schema draft-7 子集）+ 模型可见
// 描述文案。包内仅依赖标准库，不导入 internal/* 任何路径，避免任何反向依赖
// 的可能。
//
// 使用方：
//   - internal/platform/toolbridge —— codexapp host-direct 分支按本包 schema
//     注册工具到模型可见的 DynamicTools 数组
//   - internal/provider/claudecli/skill_mcp_server —— Phase 2 规划中的
//     claudecli same-binary stdio MCP server 将复用同一份 schema 暴露给 Claude CLI
//
// 字段约定与 internal/module/skill 的 ExpandBodyParams / ReadResourceParams
// 对齐；但 cwd 字段 **不暴露给模型**，由调用方 host runtime 根据当前 thread
// 上下文注入。
package skilltool

// ToolNameExpandBody 是 SKILL.md body 按需展开工具的名称常量。
//
// 命名约定：snake_case，与 P20.11 废弃文档（docs/plans/迁移/p20/p20.11-mcp-skill-tools.md）
// 中模型可见的工具名一致。
const ToolNameExpandBody = "skill_expand_body"

// ToolNameReadResource 是 skill 目录资源文件按需读取工具的名称常量。
const ToolNameReadResource = "skill_read_resource"

// DescriptionExpandBody 给模型看的工具描述。强调按需调用 + 锚点切片 + 截断行为。
const DescriptionExpandBody = "Read the body of an installed skill (SKILL.md) by name. " +
	"Use this when a skill is listed in the system prompt but its full content is not yet " +
	"in context. Optionally pass an `anchor` (Markdown H2/H3 heading) to fetch only that " +
	"section. The host returns frontmatter-stripped body text; large files are truncated to " +
	"`max_bytes` (server may apply its own cap). Trust=project skills require user approval " +
	"on first call; the tool will return an approval-required error in that case."

// DescriptionReadResource 给模型看的工具描述。强调路径安全 + 资源文件类型限制。
const DescriptionReadResource = "Read a resource file co-located with an installed skill " +
	"(e.g. references/foo.md, scripts/bar.sh). Pass the skill `name` plus the relative `path` " +
	"inside the skill directory. Path traversal (..) and absolute paths are rejected. Binary " +
	"or non-UTF-8 files are rejected; this tool returns text only. Trust=project skills " +
	"require user approval on first call."

// ExpandBodyInputSchema 是 skill_expand_body 的 InputSchema（JSON Schema draft-7 子集）。
//
// 字段对齐 internal/module/skill.ExpandBodyParams：
//   - name (required, string): skill 名称
//   - anchor (optional, string): Markdown 锚点（H2/H3 标题）
//   - max_bytes (optional, integer): 最大返回字节数；服务器有上限
//
// 注：cwd 字段**不在 schema 中**，由 host runtime 在调用前注入。
func ExpandBodyInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name as listed in the available-skills section of the system prompt.",
			},
			"anchor": map[string]any{
				"type":        "string",
				"description": "Optional Markdown H2/H3 heading to slice. Empty/omitted returns the full body.",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"description": "Optional cap on returned body bytes. Server enforces its own ceiling.",
				"minimum":     1,
			},
		},
		"required":             []string{"name"},
		"additionalProperties": false,
	}
}

// ReadResourceInputSchema 是 skill_read_resource 的 InputSchema。
//
// 字段对齐 internal/module/skill.ReadResourceParams：
//   - name (required, string): skill 名称
//   - path (required, string): 相对 skill 目录的路径，禁止 .. 与绝对路径
//   - max_bytes (optional, integer): 最大返回字节数
func ReadResourceInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Skill name owning the resource file.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Relative path inside the skill directory (e.g. `references/usage.md`). Absolute paths and `..` segments are rejected.",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"description": "Optional cap on returned content bytes. Server enforces its own ceiling.",
				"minimum":     1,
			},
		},
		"required":             []string{"name", "path"},
		"additionalProperties": false,
	}
}
