//go:build !linux && !darwin && !windows

package pidregistry

import "fmt"

func readProcessIdentity(pid int) (processIdentity, error) {
	return processIdentity{}, fmt.Errorf("pidregistry: process identity unsupported on this platform for PID %d", pid)
}
