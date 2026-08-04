package hiddenexec

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"time"
)

var (
	// ErrProcessTreeIdentityMismatch means that a PID no longer refers to the
	// immutable identity captured for this owner.
	ErrProcessTreeIdentityMismatch = errors.New("process-tree identity mismatch")
	// ErrProcessTreePidfdUnavailable is Linux-only and is deliberately not
	// recoverable by a process-group or parent-PID fallback.
	ErrProcessTreePidfdUnavailable = errors.New("process-tree pidfd unavailable")
	// ErrProcessTreeRemaining means that one or more owner members remain alive.
	ErrProcessTreeRemaining    = errors.New("process-tree members remain")
	ErrProcessTreeContextNil   = errors.New("process-tree context is nil")
	ErrProcessTreeOwnerMissing = errors.New("process-tree owner is unavailable")
)

// processTreeController 隐藏平台进程树句柄与终止细节。
type processTreeController interface {
	terminate() error
	release() error
	rssBytes() (uint64, error)
	identity() (ProcessIdentity, error)
	snapshot() (ProcessTreeSnapshot, error)
	alive() (bool, error)
	descendants() ([]ProcessIdentity, error)
	graceful(context.Context) error
	force(context.Context) error
	wait(context.Context) error
	remaining() ([]ProcessIdentity, error)
}

// ProcessIdentity 是进程在一次 ProcessTree 生命周期内的不可变身份快照。
// StartToken 与 PID 共同构成 PID 复用防护；SessionID/ProcessGroupID 约束 Unix 所有权。
// 返回值始终是副本，调用方不得把它当作当前探测结果。
type ProcessIdentity struct {
	PID            int
	StartToken     string
	UID            uint32
	SessionID      int
	ProcessGroupID int
}

// Equal 比较两个进程身份快照的全部安全绑定字段。
func (p ProcessIdentity) Equal(other ProcessIdentity) bool {
	return p == other
}

// ProcessTreeSnapshot 是一次原子进程树观察结果。
// Members 包含已知且通过身份绑定的 owner 成员；Unknown 表示无法证明属于 owner 的同组成员。
// 返回的 slice 是独立副本，后续快照不会改变此前结果。
type ProcessTreeSnapshot struct {
	Root       ProcessIdentity
	Members    []ProcessIdentity
	Unknown    []ProcessIdentity
	CapturedAt time.Time
}

// ProcessTree 显式持有一次子进程启动所对应的平台进程树 owner。
// Windows owner 持有 Job Object；Unix owner 绑定命令的独立进程组。
type ProcessTree struct {
	controller processTreeController
}

// Command 构造普通命令并套用平台隐藏窗口配置。
// Windows 下避免 LSP/安装器弹出控制台窗口，其他平台保持 exec 默认行为。
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureCommand(cmd)
	return cmd
}

// CommandContext 构造可取消命令并套用平台隐藏窗口配置。
// 调用方仍负责检查 ctx 和命令退出错误，避免吞掉安装或探测失败。
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	// Keep os/exec's standard cancellation: it terminates this exact root
	// process.  A *exec.Cmd has no immutable tree owner, so cancellation must
	// not guess a process group from the current PID.  Callers that need
	// descendant cleanup must use StartProcessTree and its explicit owner.
	return cmd
}

// StartProcessTree 启动命令并在子进程执行用户代码前建立平台进程树所有权。
// Windows 使用受控 suspended 启动；任一绑定或恢复步骤失败都会终止并回收已创建进程。
func StartProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	return startProcessTree(cmd)
}

// Terminate 执行统一的有界优雅关闭、验证和必要强制关闭事务。
func (p *ProcessTree) Terminate() error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	return p.controller.terminate()
}

// Release 释放平台进程树 owner；该操作不会隐式降级到按 PID 回收。
func (p *ProcessTree) Release() error {
	if p == nil || p.controller == nil {
		return errors.New("process-tree owner is nil")
	}
	return p.controller.release()
}

// RSSBytes 返回 owner 当前全部成员的 RSS。
func (p *ProcessTree) RSSBytes() (uint64, error) {
	if p == nil || p.controller == nil {
		return 0, errors.New("process-tree owner is nil")
	}
	return p.controller.rssBytes()
}

// Identity 返回启动时捕获的根进程身份快照。
func (p *ProcessTree) Identity() (ProcessIdentity, error) {
	if p == nil || p.controller == nil {
		return ProcessIdentity{}, errors.New("process-tree owner is nil")
	}
	return p.controller.identity()
}

// Snapshot 在 action-time 重新读取完整成员闭包并验证身份。
func (p *ProcessTree) Snapshot() (ProcessTreeSnapshot, error) {
	if p == nil || p.controller == nil {
		return ProcessTreeSnapshot{}, errors.New("process-tree owner is nil")
	}
	return p.controller.snapshot()
}

// Alive 报告根进程仍否匹配启动时身份。
func (p *ProcessTree) Alive() (bool, error) {
	if p == nil || p.controller == nil {
		return false, errors.New("process-tree owner is nil")
	}
	return p.controller.alive()
}

func slicesSortIdentities(identities []ProcessIdentity) {
	sort.Slice(identities, func(i, j int) bool { return identities[i].PID < identities[j].PID })
}

// ProcessTreeRSSBytes 汇总指定语言服务器根 PID 的平台进程组 RSS。
// Windows 必须改用显式 ProcessTree owner，避免 PID 复用和 ParentProcessID 图误计。
func ProcessTreeRSSBytes(pid int) (uint64, error) {
	return processTreeRSSBytes(pid)
}

// ProcessRSSBytes 返回指定单个 PID 的当前 RSS，不包含后代。
func ProcessRSSBytes(pid int) (uint64, error) {
	return processRSSBytes(pid)
}

// ProcessAlive 报告 PID 是否仍指向活动进程。
func ProcessAlive(pid int) (bool, error) {
	return processAlive(pid)
}

// ProcessStartIdentity 返回可区分 PID 复用的进程启动身份。
func ProcessStartIdentity(pid int) (string, error) {
	return processStartIdentity(pid)
}

// KillProcessTree 拒绝没有启动时 owner 的破坏性调用。
// 语言服务器的树必须通过 StartProcessTree 返回的 exact owner 操作；
// 不能在 action-time 依据一个裸 PID 重新捕获身份并假设它仍归属原 owner。
func KillProcessTree(cmd *exec.Cmd) error {
	return killProcessTree(cmd)
}
