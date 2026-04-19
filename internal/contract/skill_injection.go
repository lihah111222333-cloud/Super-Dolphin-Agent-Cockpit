package contract

// SkillInjectionPort 抽象 provider 层的 skill 注入决策侧效，供 turn resolver /
// prompt catalog provider 等上游模块消费。
//
// P20.1 §4 Phase 7 引入：
//   - 让 codexapp 与 claudecli 共享决策语义（例如 Mode=None 时 skill list 不显露名字）
//   - 允许 Resolver 在不直接依赖 provider 具体实现的前提下，按 provider 能力做 per-turn 决策
//
// 当前接口只定义两个方法（本 Phase 最小集）：
//   - DetectNativeSkills：报告 provider 原生机制已接管的 skill 名
//   - ReservedTokens：报告 provider 为 skill manifest 预留的 token 预算
//
// 未来 Phase 8/9 按需扩展 InjectL1Manifest / BuildTurnSection 等方法。
type SkillInjectionPort interface {
	// DetectNativeSkills 返回 provider 原生 skill 机制已经自动加载的 skill 名列表。
	//
	// 背景（P20.1 §0.5 实验 B）：Claude Code CLI 会自动发现 `.claude/skills/*/SKILL.md`，
	// 无法通过 flag 关闭。若我们的 harness 再注入同名 skill body，会造成：
	//   - 双重注入，token 翻倍
	//   - 版本不一致（原生读最新文件，我们可能读 scan 缓存）
	//
	// 修复策略：Resolver 对命中名单的 skill 强制 `Mode=None, Source=native`，
	// body 完全交给 provider 原生注入；我们的 L1 清单只标注 `(Claude native)`
	// 并提示模型使用 `/<name>` 或自然语言触发。
	//
	// codexapp 目前无原生 skill 机制，实现返回空切片即可。
	// 入参 cwd 是 session 工作目录（通常是项目根），实现侧自行决定如何解析
	// `.claude/skills/` 等相对路径。
	DetectNativeSkills(cwd string) []string

	// ReservedTokens 返回 provider 为 skill manifest（L1 清单）预留的 token 预算。
	//
	// Phase 8 SkillCatalogProvider 用来做截断决策：当候选 skill 数量 × 摘要长度
	// 超过预算时，截断到 pinned + top-k。
	//
	// P20.1 §3.7 建议默认 3000（约占 200k 上下文窗口 1.5%），可被 env
	// SKILL_MANIFEST_TOKEN_BUDGET 或 provider-specific 配置覆盖。
	ReservedTokens() int
}
