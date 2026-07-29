package providerrecoveryguard

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/providerrecovery"

type requestAlias = providerrecovery.Request

type binding struct {
	Provider         string
	RolloutPath      string
	ProviderThreadID string
	SessionUUID      string
	CodexHome        string
	ClaudeHome       string
}

func mapAliasByField(binding binding) providerrecovery.Request {
	var request requestAlias
	request.Provider = binding.Provider
	request.RolloutPath = binding.RolloutPath
	request.ProviderThreadID = binding.ProviderThreadID
	request.SessionUUID = binding.SessionUUID
	request.CodexHome = binding.CodexHome
	request.ClaudeHome = binding.ClaudeHome
	return request
}
