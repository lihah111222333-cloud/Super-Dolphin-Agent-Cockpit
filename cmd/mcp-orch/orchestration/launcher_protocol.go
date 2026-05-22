package orchestration

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Outbound RPC protocol contract for the remoteLauncher. Consolidates
// the previously inline method names, request parameter keys, and
// response alias ordering into a single authoritative definition so
// the P4 plan's "remoteLauncher request/response/method alias 合同要么
// 升格为显式 facade/DTO" (plan §62, §120, §280) becomes concrete and
// guardable. The archtest in
// internal/archtest/orchestration_launcher_protocol_guard_test.go
// pins these literals so launcher.go cannot silently drift to a new
// outbound protocol surface.
//
// Consumers (launcher.go) must reference the constants / slices here
// instead of embedding raw string literals. Response alias ordering
// is significant: the first non-empty value wins — downstream readers
// rely on that precedence to stay compatible with older peer
// implementations without inventing a new fallback chain per call.

// Method names.
const (
	// LauncherMethodThreadStart opens a remote thread and returns
	// thread/agent identity for the newly-launched managed agent.
	LauncherMethodThreadStart = contract.ThreadRPCStart
	// LauncherMethodThreadStop closes a remote thread previously
	// opened by LauncherMethodThreadStart.
	LauncherMethodThreadStop = contract.ThreadRPCStop
	// LauncherMethodThreadArchive archives a remote thread previously
	// opened by LauncherMethodThreadStart, performing the full archive
	// flow on the main app side (status=archived, binding archived,
	// scratchpad/turn cleanup, archived event publish).
	LauncherMethodThreadArchive = contract.ThreadRPCArchive
	// LauncherMethodThreadNameSet updates the display name of a
	// remote thread. Optional; only explicit rename callers should use
	// this path. Launch and turn submission must not infer names from
	// prompt text.
	LauncherMethodThreadNameSet = contract.ThreadRPCNameSet
	// LauncherMethodTurnStart submits a turn against an already-open
	// remote thread and returns the new turn identifier.
	LauncherMethodTurnStart = contract.TurnRPCStart
)

// Request parameter keys for LauncherMethodThreadStart.
const (
	LauncherParamAgentID          = "agent_id"
	LauncherParamCwd              = "cwd"
	LauncherParamName             = "name"
	LauncherParamAgentType        = "agent_type"
	LauncherParamAgentKey         = "agent_key"
	LauncherParamPromptKey        = "prompt_key"
	LauncherParamAgentMemoryScope = "agent_memory_scope"
	LauncherParamParentAgentID    = "parent_agent_id"
	LauncherParamBaseInstructions = "base_instructions"
	LauncherParamProvider         = "provider"
	LauncherParamModel            = "model"
	LauncherParamEffort           = "effort"
	LauncherParamLanguage         = "language"
	LauncherParamConfig           = "config"
	LauncherParamDisabledTools    = "disabled_tools"
)

// Request parameter keys for LauncherMethodThreadStop /
// LauncherMethodThreadArchive / LauncherMethodThreadNameSet /
// LauncherMethodTurnStart.
const (
	LauncherParamThreadID             = "thread_id"
	LauncherParamInput                = "input"
	LauncherParamSelectedSkills       = "selected_skills"
	LauncherParamManualSkillSelection = "manual_skill_selection"
	LauncherParamOutputSchema         = "output_schema"
)

// Response keys from LauncherMethodThreadStart.
const (
	LauncherRespThread         = "thread"
	LauncherRespThreadID       = "thread_id"
	LauncherRespThreadIDCamel  = "threadId"
	LauncherRespThreadNestedID = "id"
	LauncherRespAgentID        = "agent_id"
	LauncherRespAgentIDCamel   = "agentId"
	LauncherRespTurnID         = "turn_id"
)

// launcherAliasLocation picks which map a launcher response alias is
// read from: nested under the `thread` sub-object, or the top-level
// response object.
type launcherAliasLocation int

const (
	launcherAliasNested launcherAliasLocation = iota
	launcherAliasTopLevel
)

// launcherResponseAlias records one entry in a precedence list used
// to extract a field from a LauncherMethodThreadStart response. The
// ordering of each list is load-bearing — the first non-empty value
// wins — so older peer variants that emit `agentId` keep working
// alongside newer peers that emit `agent_id`.
type launcherResponseAlias struct {
	Key      string
	Location launcherAliasLocation
}

// launcherThreadStartThreadIDAliases is the precedence order used to
// extract the remote thread id from a LauncherMethodThreadStart
// response.
var launcherThreadStartThreadIDAliases = []launcherResponseAlias{
	{Key: LauncherRespThreadNestedID, Location: launcherAliasNested},  // thread.id
	{Key: LauncherRespThreadIDCamel, Location: launcherAliasTopLevel}, // threadId
	{Key: LauncherRespThreadID, Location: launcherAliasTopLevel},      // thread_id
}

// launcherThreadStartAgentIDAliases is the precedence order used to
// extract the remote agent id from a LauncherMethodThreadStart
// response.
var launcherThreadStartAgentIDAliases = []launcherResponseAlias{
	{Key: LauncherRespAgentIDCamel, Location: launcherAliasTopLevel}, // agentId
	{Key: LauncherRespAgentID, Location: launcherAliasTopLevel},      // agent_id
}

// resolveLauncherThreadStartAlias walks the configured alias ordering
// for a LauncherMethodThreadStart response and returns the first
// non-empty value. Nested aliases are read from `nested` (the thread
// sub-object); top-level aliases are read from `resp`. `fallback` is
// returned when every alias is empty; callers pass "" when an empty
// result should be treated as a protocol error.
func resolveLauncherThreadStartAlias(nested, resp map[string]any, aliases []launcherResponseAlias, fallback string) string {
	for _, alias := range aliases {
		var source map[string]any
		switch alias.Location {
		case launcherAliasNested:
			source = nested
		default:
			source = resp
		}
		if source == nil {
			continue
		}
		if v := rpcString(source[alias.Key]); v != "" {
			return v
		}
	}
	return fallback
}
