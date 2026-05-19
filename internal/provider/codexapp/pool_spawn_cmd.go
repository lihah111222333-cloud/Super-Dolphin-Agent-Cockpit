package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// PoolSpawnArgs drives BuildPoolSpawnCmd. Home is injected as CODEX_HOME,
// ExtraArgs are appended after `codex app-server`, and ParentEnv keeps the
// builder unit-testable with synthetic parent environments.
type PoolSpawnArgs struct {
	Home      string
	ExtraArgs []string
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

func localSpawnAppServerArgs() []string {
	return append(buildCodexAppServerArgs(localSpawnListenURL()), localSpawnNativeLSPFailClosedArgs()...)
}

func localSpawnNativeLSPFailClosedArgs() []string {
	return poolSpawnNativeLSPConfigOverrideArgs([]string{
		"mcp_servers.lsp.command=" + tomlString(codexLocalMCPCommand("mcp-lsp")),
		"mcp_servers.lsp.type=" + tomlString("stdio"),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString("[]"),
	})
}

func codexLocalMCPCommand(name string) string {
	if dir := strings.TrimSpace(resolveCodexLocalMCPBinaryDir()); dir != "" {
		return filepath.Join(dir, strings.TrimSpace(name))
	}
	return strings.TrimSpace(name)
}

func resolveCodexLocalMCPBinaryDir() string {
	return providershared.ResolveBinaryDir("", nil)
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
	workDir, err := poolSpawnNormalizedWorkDir(ctx)
	if err != nil {
		return nil, err
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
	extraArgs := append(poolSpawnNativeLSPConfigArgs(ctx, workDir), args.ExtraArgs...)
	argv := append(append([]string(nil), codexAppServerArgs...), extraArgs...)
	cmd := wrapWithFDLimit(argv)
	if workDir != "" {
		cmd.Dir = workDir
	}
	parent := args.ParentEnv
	if parent == nil {
		parent = os.Environ()
	}
	cmd.Env = buildPoolSpawnEnv(parent, home, workDir)
	setCodexProcessAttrs(cmd)
	return cmd, nil
}

func poolSpawnNativeLSPConfigArgs(ctx context.Context, workDir string) []string {
	roots := poolSpawnWorkspaceRoots(ctx)
	if len(roots) == 0 && strings.TrimSpace(workDir) != "" {
		roots = []string{strings.TrimSpace(workDir)}
	}
	binaryDir := strings.TrimSpace(poolSpawnMCPBinaryDir(ctx))
	if len(roots) == 0 {
		if binaryDir == "" {
			return nil
		}
		return poolSpawnNativeLSPConfigOverrideArgs([]string{
			"mcp_servers.lsp.command=" + tomlString(filepath.Join(binaryDir, "mcp-lsp")),
			"mcp_servers.lsp.type=" + tomlString("stdio"),
			"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString("[]"),
		})
	}
	primary := roots[0]
	rawRoots, err := json.Marshal(roots)
	if err != nil {
		return nil
	}
	overrides := []string{
		"mcp_servers.lsp.type=" + tomlString("stdio"),
		"mcp_servers.lsp.cwd=" + tomlString(primary),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOT=" + tomlString(primary),
		"mcp_servers.lsp.env.GO_AGENT_LSP_ROOTS=" + tomlString(string(rawRoots)),
	}
	if binaryDir != "" {
		overrides = append([]string{
			"mcp_servers.lsp.command=" + tomlString(filepath.Join(binaryDir, "mcp-lsp")),
		}, overrides...)
	}
	return poolSpawnNativeLSPConfigOverrideArgs(overrides)
}

func poolSpawnNativeLSPConfigOverrideArgs(overrides []string) []string {
	args := make([]string, 0, len(overrides)*2)
	for _, override := range overrides {
		args = append(args, "-c", override)
	}
	return args
}

func tomlString(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}
