package archtest

type sessionPrivateRuntimeException struct {
	DefinitionPath string
	CallSitePath   string
	Symbol         string
	BridgeShape    string
	ExceptionClass string
	Reason         string
	RemoveWhen     string
	RollbackWhen   string
	RollbackAction string
}

var sessionPrivateRuntimeAllowlist = []sessionPrivateRuntimeException{
	{
		DefinitionPath: "internal/provider/codexapp/session_runtime.go",
		CallSitePath:   "internal/provider/codexapp/session_runtime.go",
		Symbol:         "(*SessionRuntime).Start",
		BridgeShape:    "session_private_runtime",
		ExceptionClass: "temporary",
		Reason:         "SessionRuntime has per-session owner/join semantics but is not yet exposed as a RunnerModule adapter",
		RemoveWhen:     "SessionRuntime exposes platformrunner.Runner adapter and runtime tests cover reader/health/recovery join",
		RollbackWhen:   "new unjoined goroutine appears under (*SessionRuntime).Start",
		RollbackAction: "remove allowlist entry and require runner adapter before merge",
	},
	{
		DefinitionPath: "internal/provider/codexapp/session_runtime.go",
		CallSitePath:   "internal/provider/codexapp/session_runtime.go",
		Symbol:         "(*SessionRuntime).spawnReader",
		BridgeShape:    "session_reader",
		ExceptionClass: "temporary",
		Reason:         "reader goroutine is joined by readerDone/readerMu and remains session-private",
		RemoveWhen:     "reader loop is represented as child runner or explicit bounded worker",
		RollbackWhen:   "readerDone is not awaited in shutdown tests",
		RollbackAction: "fail session-private allowlist and move reader loop to RunnerModule",
	},
	{
		DefinitionPath: "internal/provider/codexapp/session_runtime.go",
		CallSitePath:   "internal/provider/codexapp/session_runtime.go",
		Symbol:         "(*SessionRuntime).runHealthLoop",
		BridgeShape:    "session_health",
		ExceptionClass: "temporary",
		Reason:         "health goroutine is bound to session lifetime and not process-wide",
		RemoveWhen:     "health loop becomes declarative session runner with cancel/wait contract",
		RollbackWhen:   "health goroutine escapes session cancel",
		RollbackAction: "drop allowlist and require bounded drain test",
	},
	{
		DefinitionPath: "internal/provider/codexapp/session_runtime.go",
		CallSitePath:   "internal/provider/codexapp/session_runtime.go",
		Symbol:         "(*SessionRuntime).runRecoveryWorker",
		BridgeShape:    "session_recovery",
		ExceptionClass: "temporary",
		Reason:         "recovery worker coalesces recovery signals under SessionRuntime owner",
		RemoveWhen:     "recovery worker becomes explicit RunnerModule child runner with bounded drain",
		RollbackWhen:   "recovery worker can outlive SessionRuntime Stop",
		RollbackAction: "drop allowlist and require runner adapter / shutdown test",
	},
	{
		DefinitionPath: "internal/app/app.go",
		CallSitePath:   "internal/app/app.go",
		Symbol:         "watchFXShutdown",
		BridgeShape:    "desktop_watcher",
		ExceptionClass: "permanent",
		Reason:         "desktop wails shutdown watcher: listens to app.Done() and notifies wails lifecycle; owner ctx from RunDesktop ensures bounded lifetime, stop channel + ctx.Done exit paths",
		RemoveWhen:     "n/a — permanent: desktop-only wails watcher lives outside RunGroup by design; RunDesktop owns its ctx WithCancel",
		RollbackWhen:   "new unjoined goroutine appears under watchFXShutdown or runShutdownWatcher not honoring ctx.Done",
		RollbackAction: "remove this entry and require watcher join via RunnerModule before merge",
	},
}
