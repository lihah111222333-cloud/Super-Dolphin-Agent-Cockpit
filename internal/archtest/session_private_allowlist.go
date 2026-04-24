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

type runtimeOwnershipTODO struct {
	Finding    string
	Path       string
	Symbol     string
	Owner      string
	RemoveWhen string
	Phase      string
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
}

var runtimeOwnershipTODOs = []runtimeOwnershipTODO{
	{"F-3", "internal/module/memory/module.go", "registerMemoryHooks", "P22.1-P2D", "memory scheduler/nested/teamSync workers and subscriptions migrate to RunnerModule/BusModule", "0"},
	{"F-4", "internal/module/thread/module.go", "registerSubscriptions", "P22.1-P2C", "thread bus workers/subscribers migrate to RunnerModule/BusModule", "0"},
	{"F-5", "internal/platform/cachekeepalive/module.go", "registerKeepaliveLifecycle", "P22.1-P2E", "cachekeepalive relay/timer split into BusModule/RunnerModule owners", "0"},
	{"F-9", "internal/platform/toolbridge/module.go", "registerDiffFallbackLifecycle", "P22.1-P2F", "toolbridge diff fallback subscriber migrates to BusModule", "0"},
}
