package pidregistry

import (
	"errors"
	"fmt"
	"strings"
)

type processIdentity struct {
	startToken string
	executable string
}

// readStableProcessIdentity 按 start-token、executable、start-token 顺序拒绝 generation drift。
func readStableProcessIdentity(
	pid int,
	readStartToken func(int) (string, error),
	readExecutable func(int) (string, error),
) (processIdentity, error) {
	if pid <= 1 || readStartToken == nil || readExecutable == nil {
		return processIdentity{}, fmt.Errorf("pidregistry: stable process identity readers are invalid for PID %d", pid)
	}
	before, err := readStartToken(pid)
	if err != nil {
		return processIdentity{}, err
	}
	executable, err := readExecutable(pid)
	if err != nil {
		return processIdentity{}, err
	}
	after, err := readStartToken(pid)
	if err != nil {
		return processIdentity{}, err
	}
	if strings.TrimSpace(before) == "" || strings.TrimSpace(executable) == "" {
		return processIdentity{}, errors.New("pidregistry: stable process identity is incomplete")
	}
	if before != after {
		return processIdentity{}, ErrStableProcessIdentityMismatch
	}
	return processIdentity{startToken: before, executable: executable}, nil
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
