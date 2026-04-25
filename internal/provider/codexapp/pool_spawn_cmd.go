package codexapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PoolSpawnArgs drives BuildPoolSpawnCmd. Home is the canonicalized
// codexHome the child process must operate as — the value is injected
// as CODEX_HOME in the spawned environment and wins over anything the
// parent exported. ExtraArgs are appended after `codex app-server` so
// callers can opt into flags (for example a fixed --listen address in
// tests) without bypassing the shared ulimit wrapper.
type PoolSpawnArgs struct {
	Home      string
	ExtraArgs []string
	// ParentEnv is usually os.Environ(); taking it as an argument keeps
	// the function pure and unit-testable against a synthetic parent
	// environment.
	ParentEnv []string
}

const (
	codexBinaryName       = "codex"
	codexAppServerCommand = "app-server"
	codexAppServerListen  = "--listen"
)

// codexAppServerArgs is the base command invoked by the pool spawner. The
// local transport and orphan sweeper use the same command definition helpers
// so spawn and discovery cannot drift.
var codexAppServerArgs = buildCodexAppServerArgs(localSpawnListenURL())

func buildCodexAppServerArgs(listenURL string) []string {
	return []string{codexBinaryName, codexAppServerCommand, codexAppServerListen, listenURL}
}

func isCodexAppServerListenArgs(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if commandLeaf(args[i]) == codexAppServerCommand && args[i+1] == codexAppServerListen {
			return true
		}
	}
	return false
}

func commandLeaf(arg string) string {
	if idx := strings.LastIndexAny(arg, `/\`); idx >= 0 {
		return arg[idx+1:]
	}
	return arg
}

// BuildPoolSpawnCmd assembles a *exec.Cmd that the ServerPool spawner
// Start()s. The recipe combines three plan-mandated pieces:
//
//  1. Shell wrapper for ulimit. macOS GUI-launched processes inherit
//     launchd's 256 fd soft limit, which starves batch agent scenarios
//     (100 agents × 2 MCP servers each). Running under sh -c 'ulimit
//     -n 1048576 ...; exec codex ...' guarantees the child starts with
//     a high limit regardless of launch context (.app / terminal /
//     launchd).
//  2. Env allowlist via buildAllowlistedSpawnEnv — strips everything
//     outside the closed allowlist (PATH / HOME / USER / ...) and
//     injects CODEX_HOME=<Home>. Overrides win over parent values so
//     a stale CODEX_HOME in the parent can't bleed through.
//  3. Setpgid so the child and its descendants land in a new process
//     group — orphan sweepers can then kill the whole tree by
//     negative pgid when the parent exits uncleanly.
//
// The function only builds the *exec.Cmd; starting + stderr pumping +
// listen URL parsing + transport registration remain the caller's
// responsibility. Keeping the function pure makes it straightforward
// to unit-test the env / argv / SysProcAttr shape without actually
// spawning a child.
func BuildPoolSpawnCmd(ctx context.Context, args PoolSpawnArgs) (*exec.Cmd, error) {
	home := strings.TrimSpace(args.Home)
	if home == "" {
		return nil, fmt.Errorf("codexapp: BuildPoolSpawnCmd requires a codex home")
	}
	// NOTE: ctx is intentionally NOT threaded into exec.CommandContext.
	// exec.CommandContext kills the child process when ctx is done;
	// callers typically pass a short "startup timeout" ctx that cancels
	// right after waitForListenURL returns, which would kill the freshly
	// spawned codex the moment runPoolSpawn unwinds. Process lifetime is
	// owned by transport.shutdownTransport; the startup ctx is only used
	// for the I/O-level waits further up the stack. Keeping the param
	// preserves the signature for callers that want to reject a
	// pre-cancelled ctx early.
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	argv := append([]string(nil), codexAppServerArgs...)
	if len(args.ExtraArgs) > 0 {
		argv = append(argv, args.ExtraArgs...)
	}
	cmd := wrapWithFDLimit(argv)
	parent := args.ParentEnv
	if parent == nil {
		parent = os.Environ()
	}
	cmd.Env = buildAllowlistedSpawnEnv(parent, map[string]string{"CODEX_HOME": home})
	setCodexProcessAttrs(cmd)
	return cmd, nil
}
