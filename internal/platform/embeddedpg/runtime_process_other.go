//go:build !windows

package embeddedpg

import "os/exec"

func configurePostgresCommand(_ *exec.Cmd) {}
