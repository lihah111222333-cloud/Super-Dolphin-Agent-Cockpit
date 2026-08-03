//go:build darwin || linux

package hiddenexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type unixProcessTree struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	root     ProcessIdentity
	known    map[int]ProcessIdentity
	released bool
}

func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	if cmd == nil {
		return nil, errors.New("start process tree: command is nil")
	}
	if cmd.Process != nil {
		return nil, errors.New("start process tree: command is already started")
	}
	if cmd.SysProcAttr == nil {
		configureCommand(cmd)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		return nil, errors.New("start process tree: independent session/PGID is required")
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	root, err := captureProcessIdentity(cmd.Process.Pid)
	if err != nil {
		return nil, fmt.Errorf("capture process-tree root identity: %w; cleanup pending: destructive action is not authorized", err)
	}
	if root.PID != cmd.Process.Pid || root.ProcessGroupID != root.PID || root.SessionID != root.PID {
		return nil, fmt.Errorf("process-tree root is not an independent session/PGID: %+v; cleanup pending: destructive action is not authorized", root)
	}
	owner := &unixProcessTree{cmd: cmd, root: root, known: map[int]ProcessIdentity{root.PID: root}}
	return &ProcessTree{controller: owner}, nil
}

func (p *unixProcessTree) terminate() error {
	gracefulCtx, gracefulCancel := context.WithTimeout(context.Background(), time.Second)
	defer gracefulCancel()
	if gracefulErr := p.graceful(gracefulCtx); gracefulErr != nil {
		return gracefulErr
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	waitErr := p.wait(waitCtx)
	waitCancel()
	if waitErr == nil {
		return nil
	}
	forceCtx, forceCancel := context.WithTimeout(context.Background(), time.Second)
	if forceErr := p.force(forceCtx); forceErr != nil {
		forceCancel()
		return forceErr
	}
	forceCancel()
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer finalCancel()
	return p.wait(finalCtx)
}

func (p *unixProcessTree) release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return nil
	}
	snapshot, err := p.snapshotLocked()
	if err != nil {
		return err
	}
	if len(snapshot.Members) != 0 || len(snapshot.Unknown) != 0 {
		return fmt.Errorf("release process-tree owner: %w: %d members, %d unknown", ErrProcessTreeRemaining, len(snapshot.Members), len(snapshot.Unknown))
	}
	p.released = true
	return nil
}

func (p *unixProcessTree) rssBytes() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return 0, errors.New("process-tree owner is released")
	}
	snapshot, err := p.snapshotLocked()
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, member := range snapshot.Members {
		rss, rssErr := processRSSBytes(member.PID)
		if errors.Is(rssErr, os.ErrNotExist) {
			continue
		}
		if rssErr != nil {
			return 0, fmt.Errorf("read process-tree member %d RSS: %w", member.PID, rssErr)
		}
		total += rss
	}
	if len(snapshot.Members) > 0 && total == 0 {
		return 0, errors.New("process-tree has no readable RSS")
	}
	return total, nil
}

func (p *unixProcessTree) identity() (ProcessIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return ProcessIdentity{}, errors.New("process-tree owner is released")
	}
	return p.root, nil
}

func (p *unixProcessTree) snapshot() (ProcessTreeSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

func (p *unixProcessTree) snapshotLocked() (ProcessTreeSnapshot, error) {
	if p.released {
		return ProcessTreeSnapshot{}, errors.New("process-tree owner is released")
	}
	table, err := processTable()
	if err != nil {
		return ProcessTreeSnapshot{}, err
	}
	rootCurrent, rootPresent := table[p.root.PID]
	if rootPresent && !rootCurrent.Equal(p.root) {
		return ProcessTreeSnapshot{}, fmt.Errorf("%w: root PID %d was reused", ErrProcessTreeIdentityMismatch, p.root.PID)
	}
	members := make([]ProcessIdentity, 0, len(table))
	unknown := make([]ProcessIdentity, 0)
	for pid, current := range table {
		if current.SessionID != p.root.SessionID || current.ProcessGroupID != p.root.ProcessGroupID {
			continue
		}
		if expected, known := p.known[pid]; known && !current.Equal(expected) {
			return ProcessTreeSnapshot{}, fmt.Errorf("%w: member PID %d was reused", ErrProcessTreeIdentityMismatch, pid)
		}
		if pid == p.root.PID || p.knownMemberOrDescendant(pid, table) {
			p.known[pid] = current
			members = append(members, current)
			continue
		}
		unknown = append(unknown, current)
	}
	slicesSortIdentities(members)
	slicesSortIdentities(unknown)
	return ProcessTreeSnapshot{Root: p.root, Members: members, Unknown: unknown, CapturedAt: time.Now()}, nil
}

func (p *unixProcessTree) knownMemberOrDescendant(pid int, table map[int]ProcessIdentity) bool {
	if _, ok := p.known[pid]; ok {
		return true
	}
	seen := make(map[int]struct{})
	for current := pid; ; {
		if _, ok := seen[current]; ok {
			return false
		}
		seen[current] = struct{}{}
		entry, ok := table[current]
		if !ok {
			return false
		}
		if entry.PID == p.root.PID {
			return true
		}
		parent, ok := processParent(current)
		if !ok {
			return false
		}
		if parent == p.root.PID {
			return true
		}
		current = parent
	}
}

func (p *unixProcessTree) alive() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return false, errors.New("process-tree owner is released")
	}
	current, err := captureProcessIdentity(p.root.PID)
	if isProcessGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !current.Equal(p.root) {
		return false, fmt.Errorf("%w: root PID %d", ErrProcessTreeIdentityMismatch, p.root.PID)
	}
	return true, nil
}

func (p *unixProcessTree) descendants() ([]ProcessIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.snapshotLocked()
	if err != nil {
		return nil, err
	}
	result := make([]ProcessIdentity, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		if member.PID != p.root.PID {
			result = append(result, member)
		}
	}
	return result, nil
}

func (p *unixProcessTree) remaining() ([]ProcessIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshot, err := p.snapshotLocked()
	if err != nil {
		return nil, err
	}
	if len(snapshot.Unknown) != 0 {
		return nil, fmt.Errorf("%w: %d unknown members", ErrProcessTreeIdentityMismatch, len(snapshot.Unknown))
	}
	return append([]ProcessIdentity(nil), snapshot.Members...), nil
}

func (p *unixProcessTree) graceful(ctx context.Context) error {
	return p.signal(ctx, unixGracefulSignal)
}

func (p *unixProcessTree) force(ctx context.Context) error {
	return p.signal(ctx, unixForceSignal)
}

func (p *unixProcessTree) signal(ctx context.Context, signal int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return errors.New("process-tree owner is released")
	}
	snapshot, err := p.snapshotLocked()
	if err != nil {
		return err
	}
	if len(snapshot.Unknown) != 0 {
		return fmt.Errorf("%w: unknown member prevents signal", ErrProcessTreeIdentityMismatch)
	}
	if len(snapshot.Members) == 0 {
		return nil
	}
	return signalProcessMembers(snapshot.Members, signal)
}

func (p *unixProcessTree) wait(ctx context.Context) error {
	for {
		remaining, err := p.remaining()
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %d members remain: %v", ctx.Err(), len(remaining), remaining)
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func isProcessGone(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

func killProcessTree(cmd *exec.Cmd) error {
	_ = cmd
	return ErrProcessTreeOwnerMissing
}

// ProcessTreeRSSBytes is retained for telemetry only. Destructive actions use
// the explicit owner and never reconstruct a process group from a bare PID.
func processTreeRSSBytes(pid int) (uint64, error) {
	root, err := captureProcessIdentity(pid)
	if err != nil {
		return 0, err
	}
	group, err := processTable()
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, member := range group {
		if member.SessionID != root.SessionID || member.ProcessGroupID != root.ProcessGroupID {
			continue
		}
		rss, rssErr := processRSSBytes(member.PID)
		if rssErr != nil {
			continue
		}
		total += rss
	}
	if total == 0 {
		return 0, errors.New("process-tree has no readable RSS")
	}
	return total, nil
}

func processAlive(pid int) (bool, error) {
	if pid <= 1 {
		return false, nil
	}
	identity, err := captureProcessIdentity(pid)
	if isProcessGone(err) {
		return false, nil
	}
	if err != nil {
		if errors.Is(err, syscall.EIO) {
			return false, nil
		}
		return false, err
	}
	return identity.PID == pid, nil
}

func processStartIdentity(pid int) (string, error) {
	identity, err := captureProcessIdentity(pid)
	if err != nil {
		return "", err
	}
	return identity.StartToken, nil
}
