package hooks

// hooks 当前对外暴露的生命周期拦截 topic。
// 这些字符串是 peer 订阅 wire contract，修改会影响外部 hook 客户端。
const (
	TopicSessionStart   = "agent.session.start"
	TopicTurnBefore     = "agent.turn.before"
	TopicToolBefore     = "agent.tool.before"
	TopicTaskStarting   = "agent.task.starting"
	TopicStateChange    = "agent.state.change"
	TopicTurnProgress   = "agent.turn.progress"
	TopicToolAfter      = "agent.tool.after"
	TopicTurnAfter      = "agent.turn.after"
	TopicTaskCompleting = "agent.task.completing"
	TopicTurnFailed     = "agent.turn.failed"
	TopicProcessExit    = "agent.process.exit"
)
