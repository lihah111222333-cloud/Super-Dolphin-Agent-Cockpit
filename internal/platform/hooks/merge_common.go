package hooks

import mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"

func appendUniqueLease(leases []mcp.LeaseKey, seen map[mcp.LeaseKey]struct{}, lease mcp.LeaseKey) []mcp.LeaseKey {
	if lease == (mcp.LeaseKey{}) {
		return leases
	}
	if _, ok := seen[lease]; ok {
		return leases
	}
	seen[lease] = struct{}{}
	return append(leases, lease)
}
