// Package trustedgitpolicy owns the closed environment shared by authoritative Git readers.
package trustedgitpolicy

const (
	// SourceOwnerDirectory is the repository path owning the shared trusted-Git policy closure.
	SourceOwnerDirectory = "internal/devtools/trustedgitpolicy"
	// GatePrivateSourceStem covers gate-private adapters that consume the shared policy.
	GatePrivateSourceStem = "internal/devtools/gateprivate/trusted_git"
)

// Environment returns a fresh closed environment without repository, object, config, HOME, or PATH routing.
func Environment() []string {
	return []string{
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"GIT_OPTIONAL_LOCKS=0",
		"LC_ALL=C",
	}
}
