package codexapp

import (
	"errors"
	"strings"
)

const (
	sidecarRuntimeModeEnv      = "SUPER_DOLPHIN_RUNTIME_MODE"
	sidecarRuntimeResourcesEnv = "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"
)

func ensureSidecarRuntimeContract(env []string) ([]string, error) {
	if _, ok := lookupTrimmedEnvValue(env, sidecarRuntimeModeEnv); !ok {
		return nil, errors.New("peer process requires parent sidecar runtime contract: missing SUPER_DOLPHIN_RUNTIME_MODE")
	}
	if _, ok := lookupTrimmedEnvValue(env, sidecarRuntimeResourcesEnv); !ok {
		return nil, errors.New("peer process requires parent sidecar runtime contract: missing SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR")
	}
	return env, nil
}

func lookupEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], prefix); ok {
			return value, true
		}
	}
	return "", false
}

func lookupTrimmedEnvValue(env []string, key string) (string, bool) {
	value, ok := lookupEnvValue(env, key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}
