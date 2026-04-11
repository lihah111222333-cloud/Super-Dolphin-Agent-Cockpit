package codexapp

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// spawnToolbridgePeers launches mcp-orch and mcp-lsp as independent peer
// processes with GO_AGENT_PEER_MODE=1. In peer mode, bootstrap registration
// is enabled so toolbridge can find them via FindActiveByKind + Peer.Callback.
// This is separate from MCP sidecars that codex/claude spawn via stdio.
func spawnToolbridgePeers(mgr *ServerManager) {
	go func() {
		rpcAddr := os.Getenv("GO_AGENT_CTL_RPC_ADDR")
		if rpcAddr == "" {
			rpcAddr = "127.0.0.1:8090"
		}
		for i := 0; i < 30; i++ {
			conn, err := net.DialTimeout("tcp", rpcAddr, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				break
			}
			time.Sleep(200 * time.Millisecond)
		}

		exe, err := os.Executable()
		if err != nil {
			pkglogger.Warn("server_manager: cannot resolve binary dir", "error", err)
			return
		}
		binDir := filepath.Dir(exe)

		for _, name := range []string{"mcp-orch", "mcp-lsp"} {
			binPath := filepath.Join(binDir, name)
			if _, err := os.Stat(binPath); err != nil {
				pkglogger.Warn("peer spawn: binary not found", "binary", name, "path", binPath)
				continue
			}
			stdinR, stdinW, err := os.Pipe()
			if err != nil {
				continue
			}
			cmd := exec.Command(binPath)
			cmd.Stdin = stdinR
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			cmd.Env = append(os.Environ(), "GO_AGENT_PEER_MODE=1")
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				stdinR.Close()
				stdinW.Close()
				pkglogger.Warn("peer spawn: failed", "binary", name, "error", err)
				continue
			}
			stdinR.Close()
			pkglogger.Info("peer spawn: started", "binary", name, "pid", cmd.Process.Pid, "mode", "peer")

			mgr.mu.Lock()
			mgr.peerProcs = append(mgr.peerProcs, cmd.Process)
			mgr.peerPipes = append(mgr.peerPipes, stdinW)
			if mgr.pidRegistry != nil {
				mgr.pidRegistry.Register(cmd.Process.Pid, name, nil)
			}
			mgr.mu.Unlock()

			go watchAndRestartPeer(mgr, name, cmd, stdinW)
		}
	}()
}

// watchAndRestartPeer monitors a peer process and restarts it if it exits
// unexpectedly. ServerManager.stop() closes the stdin pipe which causes a
// graceful exit — in that case we don't restart.
func watchAndRestartPeer(mgr *ServerManager, name string, cmd *exec.Cmd, stdinW *os.File) {
	for {
		err := cmd.Wait()
		exitCode := -1
		if cmd.ProcessState != nil {
			exitCode = cmd.ProcessState.ExitCode()
		}

		mgr.mu.Lock()
		shuttingDown := !mgr.ready
		mgr.mu.Unlock()
		if shuttingDown {
			pkglogger.Info("peer watch: exited during shutdown", "binary", name, "exit_code", exitCode)
			return
		}

		pkglogger.Warn("peer watch: unexpected exit, restarting",
			"binary", name, "exit_code", exitCode, "error", err)

		time.Sleep(2 * time.Second)

		newCmd, newW, err2 := restartPeer(mgr, name, cmd, stdinW)
		if err2 != nil {
			return
		}
		stdinW = newW
		cmd = newCmd
	}
}

// restartPeer spawns a new peer process and updates the manager state.
func restartPeer(mgr *ServerManager, name string, oldCmd *exec.Cmd, oldPipe *os.File) (*exec.Cmd, *os.File, error) {
	exe, err := os.Executable()
	if err != nil {
		pkglogger.Warn("peer watch: cannot resolve binary for restart", "error", err)
		return nil, nil, err
	}
	binPath := filepath.Join(filepath.Dir(exe), name)
	stdinR, newW, err := os.Pipe()
	if err != nil {
		pkglogger.Warn("peer watch: pipe failed on restart", "error", err)
		return nil, nil, err
	}
	newCmd := exec.Command(binPath)
	newCmd.Stdin = stdinR
	newCmd.Stdout = os.Stderr
	newCmd.Stderr = os.Stderr
	newCmd.Env = append(os.Environ(), "GO_AGENT_PEER_MODE=1")
	newCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = newCmd.Start(); err != nil {
		stdinR.Close()
		newW.Close()
		pkglogger.Warn("peer watch: restart failed", "binary", name, "error", err)
		return nil, nil, err
	}
	stdinR.Close()
	pkglogger.Info("peer watch: restarted", "binary", name, "pid", newCmd.Process.Pid)

	mgr.mu.Lock()
	for i, p := range mgr.peerPipes {
		if p == oldPipe {
			mgr.peerPipes[i] = newW
			break
		}
	}
	if mgr.pidRegistry != nil {
		if oldCmd.Process != nil {
			mgr.pidRegistry.Unregister(oldCmd.Process.Pid)
		}
		mgr.pidRegistry.Register(newCmd.Process.Pid, name, nil)
	}
	mgr.mu.Unlock()

	return newCmd, newW, nil
}
