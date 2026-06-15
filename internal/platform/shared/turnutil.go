package shared

import "github.com/anthropic-ai/super-agent-v3/internal/util"

// IsRemoteTurnInput delegates to util.IsRemoteTurnInput.
// IsRemoteTurnInput 判断remoteturninput是否可用。
func IsRemoteTurnInput(value string) bool { return util.IsRemoteTurnInput(value) }
