package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLaunchRequestFromExecutableBuildsForkedContextMode(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name:        "agent-forked",
		Prompt:      " inspect inherited launch flow ",
		ContextMode: " FoRkEd ",
		ParentID:    " agent-parent ",
		Provider:    " codex ",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "forked", req.ContextMode)
	require.Equal(t, "agent-parent", req.ParentID)
	require.Equal(t, "inspect inherited launch flow", req.Prompt)
}

func TestLaunchAgentRejectsForkedContextWithoutParent(t *testing.T) {
	_, err := launchRequestFromExecutable(LaunchAgentInput{Name: "agent-forked", Prompt: "inspect", ContextMode: "forked"}, "/tmp/agent-terminal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "context_mode=forked requires non-empty parent_id")
}

func TestLaunchAgentRejectsForkedContextField(t *testing.T) {
	_, err := launchRequestFromExecutable(LaunchAgentInput{
		Name: "agent-forked", Prompt: "inspect", ContextMode: "forked",
		ParentID: "agent-parent", Context: "background",
	}, "/tmp/agent-terminal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "context_mode=forked does not accept context field")
}

func TestLaunchAgentSchemaIncludesForkedContextMode(t *testing.T) {
	props := launchAgentSchemaProperties(t)
	contextMode, ok := props["context_mode"].(map[string]any)
	require.Truef(t, ok, "context_mode schema type = %T, want map[string]any", props["context_mode"])
	require.Contains(t, EnumValues(Schema(contextMode)), "forked")
}

func TestLaunchAgentRejectsForkedClaudeChild(t *testing.T) {
	_, err := launchRequestFromExecutable(LaunchAgentInput{
		Name: "agent-forked-claude-child", ParentID: "agent-parent",
		ContextMode: "forked", Provider: "claude",
	}, "/tmp/agent-terminal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Claude sub-agent orchestration is not supported")
	require.Contains(t, err.Error(), "provider=codex")
}

func TestForkedChildKeepsDefaultDisabledDelegationTools(t *testing.T) {
	req, err := launchRequestFromExecutable(LaunchAgentInput{
		Name: "agent-forked-deny", ParentID: "agent-parent",
		ContextMode: "forked", Provider: "codex",
	}, "/tmp/agent-terminal")
	require.NoError(t, err)
	require.Equal(t, "forked", req.ContextMode)
	require.Equal(t, "launch_agent,orchestration_launch_agent,spawn_agent", launchEnvValue(req.Env, "AGENT_DISABLED_TOOLS"))
	require.Equal(t, "spawn_agent", launchEnvValue(req.Env, "AGENT_CODEX_DISABLED_NATIVE_TOOLS"))
}
