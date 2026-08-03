//go:build darwin || linux

package processprobe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

const processQueryBinary = "ps"

type processQuery struct {
	pid            int
	parentPID      int
	processGroupID string
	sessionID      string
	uid            string
	startIdentity  string
	executable     string
}

// Probe performs signal-zero liveness and a fixed, read-only ps query.
func Probe(ctx context.Context, pid int) (Snapshot, error) {
	if snapshot, err := validateProbeInput(ctx, pid); err != nil {
		return snapshot, err
	}
	alive, permissionDenied, err := signalZero(pid)
	if err != nil {
		return livenessFailure(pid, alive, permissionDenied, err)
	}
	if !alive {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonUnknown, []string{"alive", "start_identity"}, "process is not alive"), fmt.Errorf("process PID %d is not alive", pid)
	}
	return querySnapshot(ctx, pid)
}

func validateProbeInput(ctx context.Context, pid int) (Snapshot, error) {
	if ctx == nil {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"context"}, "context is nil"), errors.New("process probe context is nil")
	}
	if pid <= 1 {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"pid"}, "pid must be greater than one"), fmt.Errorf("process probe PID %d is invalid", pid)
	}
	if err := ctx.Err(); err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", false, runtime.GOOS, ReasonProbeFailed, []string{"context"}, "probe cancelled"), err
	}
	return Snapshot{}, nil
}

func livenessFailure(pid int, alive, permissionDenied bool, err error) (Snapshot, error) {
	reason := ReasonProbeFailed
	if permissionDenied {
		reason = ReasonPermissionDenied
	}
	return newSnapshot(pid, 0, "", "", "", "", "", alive, runtime.GOOS, reason, []string{"liveness"}, safeProbeError(err)), err
}

func querySnapshot(ctx context.Context, pid int) (Snapshot, error) {
	query, err := readProcessQuery(ctx, pid)
	if err != nil {
		return newSnapshot(pid, 0, "", "", "", "", "", true, runtime.GOOS, ReasonProbeFailed, []string{"process_query"}, safeProbeError(err)), err
	}
	if runtime.GOOS == "linux" {
		return linuxSnapshot(query, pid)
	}
	return queryToSnapshot(query), nil
}

func linuxSnapshot(query processQuery, pid int) (Snapshot, error) {
	startIdentity, err := linuxStartIdentity(pid)
	if err != nil {
		return newSnapshot(query.pid, query.parentPID, query.processGroupID, query.sessionID, query.uid, "", query.executable, true, runtime.GOOS, ReasonProbeFailed, []string{"start_identity"}, safeProbeError(err)), err
	}
	query.startIdentity = startIdentity
	return queryToSnapshot(query), nil
}

func queryToSnapshot(query processQuery) Snapshot {
	return newSnapshot(
		query.pid,
		query.parentPID,
		query.processGroupID,
		query.sessionID,
		query.uid,
		query.startIdentity,
		query.executable,
		true,
		runtime.GOOS,
		"",
		nil,
		"",
	)
}

func signalZero(pid int) (alive bool, permissionDenied bool, retErr error) {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return true, false, nil
	case errors.Is(err, syscall.EPERM):
		return true, true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, false, nil
	default:
		return false, false, err
	}
}

func readProcessQuery(ctx context.Context, pid int) (processQuery, error) {
	sessionField := "sid="
	if runtime.GOOS == "darwin" {
		sessionField = "sess="
	}
	command := exec.CommandContext(ctx, processQueryBinary, "-p", strconv.Itoa(pid), "-o", "pid=,ppid=,pgid=,"+sessionField+",uid=,lstart=,comm=")
	output, err := command.Output()
	if err != nil {
		return processQuery{}, err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 10 {
		return processQuery{}, errors.New("process query returned incomplete fields")
	}
	parsedPID, err := strconv.Atoi(fields[0])
	if err != nil {
		return processQuery{}, errors.New("process query PID is invalid")
	}
	parentPID, err := strconv.Atoi(fields[1])
	if err != nil {
		return processQuery{}, errors.New("process query parent PID is invalid")
	}
	if _, err := strconv.Atoi(fields[2]); err != nil {
		return processQuery{}, errors.New("process query process group is invalid")
	}
	if _, err := strconv.Atoi(fields[3]); err != nil {
		return processQuery{}, errors.New("process query session is invalid")
	}
	if _, err := strconv.Atoi(fields[4]); err != nil {
		return processQuery{}, errors.New("process query UID is invalid")
	}
	startIdentity := strings.Join(fields[5:10], " ")
	executable := filepath.Base(strings.TrimSpace(strings.Join(fields[10:], " ")))
	if !validExecutable(executable) {
		return processQuery{}, errors.New("process query executable is empty")
	}
	return processQuery{
		pid:            parsedPID,
		parentPID:      parentPID,
		processGroupID: fields[2],
		sessionID:      fields[3],
		uid:            fields[4],
		startIdentity:  startIdentity,
		executable:     executable,
	}, nil
}

func validExecutable(value string) bool { return value != "." && value != "" }

func linuxStartIdentity(pid int) (string, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	closeParen := strings.LastIndexByte(string(payload), ')')
	if closeParen < 0 || closeParen+1 >= len(payload) {
		return "", errors.New("Linux process stat is malformed")
	}
	fields := strings.Fields(string(payload)[closeParen+1:])
	const startTimeIndexAfterCommand = 19
	if len(fields) <= startTimeIndexAfterCommand {
		return "", errors.New("Linux process stat has no start time")
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	boot := strings.TrimSpace(string(bootID))
	if boot == "" {
		return "", errors.New("Linux boot identity is empty")
	}
	return boot + "/" + fields[startTimeIndexAfterCommand], nil
}

func safeProbeError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.Join(strings.Fields(err.Error()), " ")
	if len(text) > 256 {
		return text[:256]
	}
	return text
}
