package codexapp

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// cleanOrphanedAppServers finds "codex app-server --listen" processes that
// are NOT part of the current application process tree and kills them along
// with all their descendant processes (typically mcp-server-postgres,
// exa-mcp-server, etc.). Returns the total number of processes killed.
func cleanOrphanedAppServers() int {
	allProcs, appServerProcs := discoverAppServerProcesses()
	if len(appServerProcs) == 0 {
		return 0
	}

	myTree := buildProcessTree(os.Getpid(), allProcs)

	killed := 0
	for _, proc := range appServerProcs {
		if proc.pid <= 1 {
			continue
		}
		if _, inTree := myTree[proc.pid]; inTree {
			continue
		}
		killed += killOrphanTree(proc, allProcs)
	}
	if killed > 0 {
		pkglogger.Info("orphan sweeper: summary", "total_killed", killed)
	}
	return killed
}

// killOrphanTree kills an orphaned app-server process and all its descendants.
// Order: SIGTERM the app-server first so it can flush rollout JSONL to disk,
// then kill any remaining descendant processes.
// Returns the number of processes successfully killed.
func killOrphanTree(proc appServerProcessInfo, allProcs map[int]int) int {
	killed := 0

	// Step 1: SIGTERM the app-server so it can persist rollout files.
	if err := killAppServerProcess(proc.pid); err != nil {
		pkglogger.Warn("orphan sweeper: kill app-server failed",
			"pid", proc.pid, "ppid", proc.ppid, "error", err)
		return killed
	}
	pkglogger.Info("orphan sweeper: killed orphaned app-server",
		"pid", proc.pid, "ppid", proc.ppid)
	killed++

	// Step 2: Kill remaining descendants (mcp-server-postgres, exa-mcp-server, etc.)
	// that survived the app-server shutdown.
	descendants := buildProcessTree(proc.pid, allProcs)
	for descPID := range descendants {
		if descPID == proc.pid || descPID <= 1 {
			continue
		}
		// Check if still alive before killing.
		if err := syscall.Kill(descPID, 0); err != nil {
			continue // already exited with parent
		}
		if err := killMCPProcess(descPID); err != nil {
			pkglogger.Warn("orphan sweeper: kill descendant failed",
				"parent_pid", proc.pid, "descendant_pid", descPID, "error", err)
			continue
		}
		pkglogger.Info("orphan sweeper: killed orphaned descendant",
			"parent_pid", proc.pid, "descendant_pid", descPID)
		killed++
	}
	return killed
}

// appServerProcessInfo holds PID and PPID for a discovered codex app-server process.
type appServerProcessInfo struct {
	pid  int
	ppid int
}

// discoverAppServerProcesses runs `ps -eo pid,ppid,args` and returns:
//   - allProcs: map[pid]ppid for the entire process table
//   - appServers: filtered list of "codex app-server --listen" entries
func discoverAppServerProcesses() (allProcs map[int]int, appServers []appServerProcessInfo) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,args").Output()
	if err != nil {
		pkglogger.Warn("orphan sweeper: ps command failed", "error", err)
		return nil, nil
	}

	allProcs = make(map[int]int, 256)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		allProcs[pid] = ppid

		// Check if this is a "codex app-server --listen" process.
		// The args pattern is: codex app-server --listen ws://...
		if len(fields) >= 5 && isAppServerArgs(fields[2:]) {
			appServers = append(appServers, appServerProcessInfo{pid: pid, ppid: ppid})
		}
	}
	return allProcs, appServers
}

// isAppServerArgs checks whether the process arguments match
// "codex app-server --listen ws://...".
// We look for the pattern in the args slice: [..., "app-server", "--listen", ws://...]
func isAppServerArgs(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		base := args[i]
		if idx := strings.LastIndex(base, "/"); idx >= 0 {
			base = base[idx+1:]
		}
		if base == "app-server" && i+1 < len(args) && args[i+1] == "--listen" {
			return true
		}
	}
	return false
}

// killAppServerProcess gracefully terminates a codex app-server process.
// First sends SIGTERM; if it doesn't exit within the grace period, sends SIGKILL.
func killAppServerProcess(pid int) error {
	if pid <= 1 {
		return nil
	}

	// Try SIGTERM first for graceful shutdown.
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if err == syscall.ESRCH {
			return nil // already gone
		}
		// Fall through to SIGKILL below.
	} else {
		// Wait briefly for graceful exit.
		deadline := time.Now().Add(appServerKillGracePeriod)
		for time.Now().Before(deadline) {
			if err := syscall.Kill(pid, 0); err != nil {
				return nil // process exited
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	// Force kill.
	return killMCPProcess(pid)
}

const appServerKillGracePeriod = 5 * time.Second
