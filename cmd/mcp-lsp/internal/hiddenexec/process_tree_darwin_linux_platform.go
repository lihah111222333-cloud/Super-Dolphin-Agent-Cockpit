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

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

var ErrProcessTreeCleanupPending = errors.New("CleanupPending: process-tree destructive action is blocked")

type unixProcessTree struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	root          ProcessIdentity
	known         map[int]ProcessIdentity
	signalMembers func([]ProcessIdentity, int) error
	releaseOwner  func() error
	released      bool
}

func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	return startProcessTreeWithHooks(cmd, startupAbortHooks{
		captureIdentity: captureProcessIdentity,
		groupOwned:      startupProcessGroupOwned,
		startWait:       startStartupProcessWait,
		waitTimeout:     3 * time.Second,
	})
}

func startProcessTreeWithHooks(cmd *exec.Cmd, hooks startupAbortHooks) (*ProcessTree, error) {
	if err := validateProcessTreeCommand(cmd); err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	root, err := hooks.captureIdentity(cmd.Process.Pid)
	if err != nil {
		cleanupErr, startupOwner := abortStartedProcessTree(cmd, hooks, nil)
		return startupOwner, errors.Join(fmt.Errorf("capture process-tree root identity: %w", err), cleanupErr)
	}
	if err := validateProcessTreeRoot(root, cmd.Process.Pid); err != nil {
		cleanupErr, startupOwner := abortStartedProcessTree(cmd, hooks, &root)
		return startupOwner, errors.Join(err, cleanupErr)
	}
	owner := &unixProcessTree{cmd: cmd, root: root, known: map[int]ProcessIdentity{root.PID: root}, signalMembers: signalProcessMembers}
	return &ProcessTree{controller: owner}, nil
}

// validateProcessTreeCommand 确认 Unix 命令使用独立 session/PGID 后才允许建立 owner。
func validateProcessTreeCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("start process tree: command is nil")
	}
	if cmd.Process != nil {
		return errors.New("start process tree: command is already started")
	}
	if cmd.SysProcAttr == nil {
		configureCommand(cmd)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		return errors.New("start process tree: independent session/PGID is required")
	}
	return nil
}

func validateProcessTreeRoot(root ProcessIdentity, pid int) error {
	if root.PID != pid || root.ProcessGroupID != root.PID || root.SessionID != root.PID {
		return fmt.Errorf("process-tree root is not an independent session/PGID: %+v", root)
	}
	return nil
}

func (p *unixProcessTree) terminate() error {
	gracefulCtx, gracefulCancel := platformconfig.WithTimeout(context.Background(), time.Second)
	defer gracefulCancel()
	if gracefulErr := p.graceful(gracefulCtx); gracefulErr != nil {
		return gracefulErr
	}
	waitCtx, waitCancel := platformconfig.WithTimeout(context.Background(), time.Second)
	waitErr := p.wait(waitCtx)
	waitCancel()
	if waitErr == nil {
		return nil
	}
	forceCtx, forceCancel := platformconfig.WithTimeout(context.Background(), time.Second)
	if forceErr := p.force(forceCtx); forceErr != nil {
		forceCancel()
		return forceErr
	}
	forceCancel()
	finalCtx, finalCancel := platformconfig.WithTimeout(context.Background(), 3*time.Second)
	defer finalCancel()
	return p.wait(finalCtx)
}

// release 仅在进程树无存活或身份不确定成员时释放平台稳定 owner 能力。
func (p *unixProcessTree) release() error {
	p.mu.Lock()
	if p.released {
		p.mu.Unlock()
		return nil
	}
	snapshot, err := p.snapshotLocked()
	if err != nil {
		p.mu.Unlock()
		return errors.Join(ErrProcessTreeCleanupPending, err)
	}
	if len(snapshot.Members) != 0 || len(snapshot.Unknown) != 0 {
		p.mu.Unlock()
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("release process-tree owner: %w: %d members, %d unknown", ErrProcessTreeRemaining, len(snapshot.Members), len(snapshot.Unknown)))
	}
	releaseOwner := p.releaseOwner
	p.mu.Unlock()
	if releaseOwner != nil {
		if err := releaseOwner(); err != nil {
			return errors.Join(ErrProcessTreeCleanupPending, err)
		}
	}
	p.mu.Lock()
	p.released = true
	p.mu.Unlock()
	return nil
}

// prepareShutdown 在根仍存活时建立一次 non-destructive 后代身份入册。
// 根退出后不再接受无法由已知 parent-chain 证明的新成员，保持 unknown 零信号。
func (p *unixProcessTree) prepareShutdown() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return errors.New("process-tree owner is released")
	}
	table, err := processTable()
	if err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, err)
	}
	rootCurrent, rootPresent := table[p.root.PID]
	if !rootPresent {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w: root PID %d is no longer alive", ErrProcessTreeIdentityMismatch, p.root.PID))
	}
	if !rootCurrent.Equal(p.root) {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w: root PID %d was reused", ErrProcessTreeIdentityMismatch, p.root.PID))
	}
	snapshot, err := p.snapshotFromTable(table)
	if err != nil {
		return errors.Join(ErrProcessTreeCleanupPending, err)
	}
	if len(snapshot.Unknown) != 0 {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w: unknown member prevents shutdown preparation", ErrProcessTreeIdentityMismatch))
	}
	return nil
}

// rssBytes 读取当前 exact owner 成员的 RSS，并拒绝不完整的成员闭包。
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
	if err := p.validateSnapshotRoot(table); err != nil {
		return ProcessTreeSnapshot{}, err
	}
	return p.snapshotFromTable(table)
}

// snapshotFromTable 从同一进程表构造已绑定成员与未知同组成员的快照。
func (p *unixProcessTree) snapshotFromTable(table map[int]ProcessIdentity) (ProcessTreeSnapshot, error) {
	members := make([]ProcessIdentity, 0, len(table))
	unknown := make([]ProcessIdentity, 0)
	for pid, current := range table {
		if !sameProcessTreeGroup(current, p.root) {
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

func (p *unixProcessTree) validateSnapshotRoot(table map[int]ProcessIdentity) error {
	rootCurrent, rootPresent := table[p.root.PID]
	if rootPresent && !rootCurrent.Equal(p.root) {
		return fmt.Errorf("%w: root PID %d was reused", ErrProcessTreeIdentityMismatch, p.root.PID)
	}
	return nil
}

func sameProcessTreeGroup(current, root ProcessIdentity) bool {
	return current.SessionID == root.SessionID && current.ProcessGroupID == root.ProcessGroupID
}

// knownMemberOrDescendant 通过 known 身份或完整 parent-chain 证明成员属于 owner。
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
		if _, ok := p.known[current]; ok {
			return true
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
		return nil, errors.Join(ErrProcessTreeCleanupPending, err)
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
		return nil, errors.Join(ErrProcessTreeCleanupPending, err)
	}
	if len(snapshot.Unknown) != 0 {
		return nil, errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w: %d unknown members", ErrProcessTreeIdentityMismatch, len(snapshot.Unknown)))
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
		return errors.Join(ErrProcessTreeCleanupPending, err)
	}
	if len(snapshot.Unknown) != 0 {
		return errors.Join(ErrProcessTreeCleanupPending, fmt.Errorf("%w: unknown member prevents signal", ErrProcessTreeIdentityMismatch))
	}
	return p.signalSnapshot(snapshot.Members, signal)
}

func (p *unixProcessTree) signalSnapshot(members []ProcessIdentity, signal int) error {
	if len(members) == 0 {
		return nil
	}
	if p.signalMembers == nil {
		return errors.Join(ErrProcessTreeCleanupPending, errors.New("process-tree signal authority is unavailable"))
	}
	return p.signalMembers(members, signal)
}

// wait 在有界上下文内持续验证 exact owner 成员，直到全部退出或返回剩余证据。
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

// processTreeRSSBytes 仅用于遥测汇总同一 session/PGID 的 RSS，不提供破坏性回收授权。
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
