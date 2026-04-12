package skill

import (
	"encoding/json"
	"strings"
)

type execParams struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"-"`
}

type execParamsWire struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	CWD     string            `json:"cwd,omitempty"`
	Argv    []string          `json:"argv,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func (p *execParams) UnmarshalJSON(data []byte) error {
	var wire execParamsWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	next := execParams{
		Command: wire.Command,
		Args:    append([]string(nil), wire.Args...),
		CWD:     wire.CWD,
		Env:     cloneExecEnv(wire.Env),
	}
	if strings.TrimSpace(next.Command) == "" && len(wire.Argv) > 0 {
		next.Command, next.Args = splitLegacyArgv(wire.Argv)
	}
	*p = next
	return nil
}

func splitLegacyArgv(argv []string) (string, []string) {
	if len(argv) == 0 {
		return "", nil
	}
	return argv[0], append([]string(nil), argv[1:]...)
}

func cloneExecEnv(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
