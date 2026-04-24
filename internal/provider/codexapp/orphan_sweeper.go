package codexapp

import (
	"os"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// cleanOrphanedAppServers finds "codex app-server --listen" processes that
// are NOT part of the current application process tree and kills them along
// with all their descendant processes (typically mcp-server-postgres,
// exa-mcp-server, etc.). Returns the total number of processes killed.
//
// All orphaned app-servers are SIGTERM'd concurrently (batch), then we wait
// once for the grace period before SIGKILL'ing survivors. This keeps cleanup
// time constant (~5s) regardless of how many orphans exist.
func cleanOrphanedAppServers() int {
	allProcs, appServerProcs := discoverAppServerProcesses()
	if len(appServerProcs) == 0 {
		return 0
	}

	myTree := buildProcessTree(os.Getpid(), allProcs)
	orphans := filterOrphanAppServers(appServerProcs, myTree)
	if len(orphans) == 0 {
		return 0
	}

	sigtermed := sigtermAppServers(orphans)
	waitForAppServerExit(sigtermed)
	killed := sigkillAppServerSurvivors(sigtermed, allProcs)

	if killed > 0 {
		pkglogger.Info("orphan sweeper: summary", "total_killed", killed)
	}
	return killed
}

func filterOrphanAppServers(procs []appServerProcessInfo, myTree map[int]struct{}) []appServerProcessInfo {
	orphans := make([]appServerProcessInfo, 0, len(procs))
	for _, proc := range procs {
		if proc.pid <= 1 {
			continue
		}
		if _, inTree := myTree[proc.pid]; !inTree {
			orphans = append(orphans, proc)
		}
	}
	return orphans
}

func sigtermAppServers(orphans []appServerProcessInfo) []appServerProcessInfo {
	sigtermed := make([]appServerProcessInfo, 0, len(orphans))
	for _, proc := range orphans {
		if err := sendSignalToPID(proc.pid, sigTerminate); err != nil {
			if !isProcessGoneErr(err) {
				pkglogger.Warn("orphan sweeper: SIGTERM failed",
					"pid", proc.pid, "ppid", proc.ppid, "error", err)
			}
			continue
		}
		sigtermed = append(sigtermed, proc)
	}
	return sigtermed
}

func waitForAppServerExit(sigtermed []appServerProcessInfo) {
	deadline := time.Now().Add(appServerKillGracePeriod)
	for time.Now().Before(deadline) {
		allGone := true
		for _, proc := range sigtermed {
			if isProcessAlive(proc.pid) {
				allGone = false
				break
			}
		}
		if allGone {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func sigkillAppServerSurvivors(sigtermed []appServerProcessInfo, allProcs map[int]int) int {
	killed := 0
	for _, proc := range sigtermed {
		if !isProcessAlive(proc.pid) {
			pkglogger.Info("orphan sweeper: killed orphaned app-server",
				"pid", proc.pid, "ppid", proc.ppid)
			killed++
		} else if err := killMCPProcess(proc.pid); err != nil {
			pkglogger.Warn("orphan sweeper: kill app-server failed",
				"pid", proc.pid, "ppid", proc.ppid, "error", err)
			continue
		} else {
			pkglogger.Info("orphan sweeper: killed orphaned app-server",
				"pid", proc.pid, "ppid", proc.ppid)
			killed++
		}
		killed += killDescendants(proc, allProcs)
	}
	return killed
}

// killDescendants kills remaining descendant processes of an app-server
// (mcp-server-postgres, exa-mcp-server, etc.) that survived the parent shutdown.
func killDescendants(proc appServerProcessInfo, allProcs map[int]int) int {
	killed := 0
	descendants := buildProcessTree(proc.pid, allProcs)
	for descPID := range descendants {
		if descPID == proc.pid || descPID <= 1 {
			continue
		}
		if !isProcessAlive(descPID) {
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

// discoverAppServerProcesses delegates to the platform-specific process
// enumerator (Unix: `ps`, Windows: no-op for Phase 1).
func discoverAppServerProcesses() (allProcs map[int]int, appServers []appServerProcessInfo) {
	return discoverAppServerProcessList()
}

const appServerKillGracePeriod = 5 * time.Second
