package thread

import (
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const testLocalCodexModelProvider = "openai"

func attachStartedCodexRuntimeIdentityForTest(req dto.StartSessionRequest, session contract.Session) contract.Session {
	if !strings.EqualFold(strings.TrimSpace(req.Provider), "codex") || session == nil {
		return session
	}
	stub, ok := session.(*stubSession)
	if !ok {
		return session
	}
	stub.runtimeConfig = mergeCodexRuntimeIdentityForTest(req.Config, stub.runtimeConfig)
	return stub
}

func mergeCodexRuntimeIdentityForTest(startConfig, runtimeConfig map[string]any) map[string]any {
	out := make(map[string]any, len(runtimeConfig)+3)
	for key, value := range runtimeConfig {
		out[key] = value
	}
	putMissingCodexRuntimeValueForTest(out, startConfig, contract.CodexHomeKey, os.TempDir())
	putMissingCodexRuntimeValueForTest(out, startConfig, contract.CodexInstanceKeyKey, defaultCodexInstanceKey)
	putMissingCodexRuntimeValueForTest(out, startConfig, contract.CodexModelProviderKey, testLocalCodexModelProvider)
	return out
}

func putMissingCodexRuntimeValueForTest(runtimeConfig, startConfig map[string]any, key string, fallback string) {
	if raw, ok := runtimeConfig[key]; ok {
		if text, ok := raw.(string); !ok || strings.TrimSpace(text) != "" {
			return
		}
	}
	if raw, ok := startConfig[key]; ok {
		if text, ok := raw.(string); !ok || strings.TrimSpace(text) != "" {
			runtimeConfig[key] = raw
			return
		}
	}
	runtimeConfig[key] = fallback
}
