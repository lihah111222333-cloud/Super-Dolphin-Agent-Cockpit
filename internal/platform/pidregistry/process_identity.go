package pidregistry

import "strings"

type processIdentity struct {
	startToken string
	executable string
}

func processIdentityMatches(want, got processIdentity) bool {
	return strings.TrimSpace(want.startToken) != "" &&
		strings.TrimSpace(want.executable) != "" &&
		want.startToken == got.startToken &&
		want.executable == got.executable
}

func childIdentityMatches(child ChildInfo, got processIdentity) bool {
	return processIdentityMatches(processIdentity{
		startToken: child.ProcessStartToken,
		executable: child.ExecutableIdentity,
	}, got)
}
