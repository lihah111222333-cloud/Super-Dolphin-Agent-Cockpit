package thread

import (
	"os"
	"strings"
)

func buildLaunchRequest(agentID, cwd, name, provider, model string) (LaunchAgentRequest, error) {
	exe, err := os.Executable()
	if err != nil {
		return LaunchAgentRequest{}, err
	}
	return LaunchAgentRequest{
		AgentID: strings.TrimSpace(agentID),
		Name:    strings.TrimSpace(name),
		Cwd:     strings.TrimSpace(cwd),
		Command: []string{exe},
		Env:     launchConfigEnv(provider, model),
	}, nil
}

func launchConfigEnv(provider, model string) []string {
	var env []string
	if provider = strings.TrimSpace(provider); provider != "" {
		env = append(env, "AGENT_PROVIDER="+provider)
	}
	if model = strings.TrimSpace(model); model != "" {
		env = append(env, "AGENT_MODEL="+model)
	}
	return env
}
