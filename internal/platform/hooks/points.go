package hooks

// Hook topic constants for the current lifecycle interception surface.
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
