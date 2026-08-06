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

// sessionPrivateRuntimeAllowlist 返回 session-private runtime 例外的独立快照。
// 调用方只能在本次检查中使用返回值，不能修改共享 allowlist 状态。
func sessionPrivateRuntimeAllowlist() []sessionPrivateRuntimeException {
	return []sessionPrivateRuntimeException{
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
			Symbol:         "(*SessionRuntime).safeRunHealthLoop",
			BridgeShape:    "session_health",
			ExceptionClass: "temporary",
			Reason:         "health goroutine is bound to session lifetime and not process-wide; wrapped with panic recovery",
			RemoveWhen:     "health loop becomes declarative session runner with cancel/wait contract",
			RollbackWhen:   "health goroutine escapes session cancel",
			RollbackAction: "drop allowlist and require bounded drain test",
		},
		{
			DefinitionPath: "internal/provider/codexapp/session_runtime.go",
			CallSitePath:   "internal/provider/codexapp/session_runtime.go",
			Symbol:         "(*SessionRuntime).safeRunRecoveryWorker",
			BridgeShape:    "session_recovery",
			ExceptionClass: "temporary",
			Reason:         "recovery worker coalesces recovery signals under SessionRuntime owner; wrapped with panic recovery",
			RemoveWhen:     "recovery worker becomes explicit RunnerModule child runner with bounded drain",
			RollbackWhen:   "recovery worker can outlive SessionRuntime Stop",
			RollbackAction: "drop allowlist and require runner adapter / shutdown test",
		},
		{
			DefinitionPath: "internal/app/runner.go",
			CallSitePath:   "internal/app/runner.go",
			Symbol:         "BindRuntime",
			BridgeShape:    "root_bridge_runtime",
			ExceptionClass: "permanent",
			Reason:         "app root runtime bridge starts the single platformrunner.RunGroup actor under the application-owned root context in fx.Hook.OnStart; OnStop cancels and waits for the buffered done channel",
			RemoveWhen:     "n/a — permanent: BindRuntime is the root RunnerModule bridge for headless and desktop app entries",
			RollbackWhen:   "BindRuntime starts additional unjoined goroutines, stops using RootCtxProvider, or stops waiting for RunGroup completion on shutdown",
			RollbackAction: "remove this entry and require BindRuntime to keep all runtime actors inside platformrunner.RunGroup with explicit stop/join tests",
		},
	}
}
