package skill

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

// skillsChangedEmitter 是 UI skill 变更事件派发函数，测试可替换为捕获器。
type skillsChangedEmitter func(uidto.SkillsChanged)

const (
	skillsChangedDebounceWindow = 100 * time.Millisecond
	skillsChangedStoppedError   = "skill debounce runner stopped"
)

type skillsChangedHealthSnapshot struct {
	Stopped          bool
	DroppedAfterStop uint64
	LastError        string
	Pending          int
}

func (s *service) skillsChangedHealthSnapshot() skillsChangedHealthSnapshot {
	if s == nil {
		return skillsChangedHealthSnapshot{}
	}
	s.skillsChangedMu.Lock()
	defer s.skillsChangedMu.Unlock()
	pending := len(s.skillsChangedQueue)
	if s.skillsChangedNext.Count > 0 {
		pending++
	}
	return skillsChangedHealthSnapshot{
		Stopped:          s.skillsChangedStopped,
		DroppedAfterStop: s.skillsChangedDropped,
		LastError:        s.skillsChangedLastError,
		Pending:          pending,
	}
}

// bindDispatcher 绑定 UI 事件派发器，service 为空时保持无操作以便测试装配复用。
func (s *service) bindDispatcher(dispatcher *event.Dispatcher) {
	if s == nil {
		return
	}
	s.emitSkillsChanged = contract.NewEmitter[uidto.SkillsChanged](dispatcher)
}

// publishSkillsChanged 发布不区分 personal type 的 skill 变更事件。
func (s *service) publishSkillsChanged(ctx context.Context, action, name, scope string) {
	s.publishSkillsChangedForPersonalType(ctx, action, name, scope, "")
}

// publishSkillsChangedForPersonalType 规范化变更元数据，并交给 debounce 队列合并发送。
func (s *service) publishSkillsChangedForPersonalType(ctx context.Context, action, name, scope, personalType string) {
	if s == nil || s.emitSkillsChanged == nil {
		return
	}
	normalizedScope := strings.TrimSpace(scope)
	normalizedPersonalType := strings.TrimSpace(personalType)
	repoFingerprint, relativePath := s.skillsChangedLocation(ctx, normalizedScope)
	s.scheduleSkillsChanged(uidto.SkillsChanged{
		EventHeader:     shared.EventHeader{Timestamp: time.Now()},
		Name:            strings.TrimSpace(name),
		Action:          normalizeSkillsChangedAction(action),
		Count:           1,
		Scope:           normalizedScope,
		PersonalType:    normalizedPersonalType,
		RepoFingerprint: repoFingerprint,
		RelativePath:    relativePath,
	})
}

// scheduleSkillsChanged 将 skill 变更放入短窗口 debounce。
// 同一 scope/personal/project 位置的变更会合并，不同位置的事件会按顺序排队发送。
func (s *service) scheduleSkillsChanged(next uidto.SkillsChanged) {
	next = normalizeSkillsChanged(next)

	s.skillsChangedMu.Lock()
	if s.skillsChangedStopped {
		s.skillsChangedDropped++
		s.skillsChangedLastError = skillsChangedStoppedError
		s.skillsChangedMu.Unlock()
		return
	}
	if s.skillsChangedNext.Count == 0 {
		s.skillsChangedNext = next
	} else if skillsChangedMergeable(s.skillsChangedNext, next) {
		s.skillsChangedNext = mergeSkillsChanged(s.skillsChangedNext, next)
	} else {
		// 跨 scope 或跨 cwd 的事件不能共享一个 payload，否则订阅方无法判断要刷新哪一侧。
		// 先把当前缓冲事件排队，再用 next 开新缓冲。
		s.skillsChangedQueue = append(s.skillsChangedQueue, s.skillsChangedNext)
		s.skillsChangedNext = next
	}
	s.skillsChangedSeq.Add(1)
	s.skillsChangedMu.Unlock()

	select {
	case s.skillsChangedWake <- struct{}{}:
	default:
	}
}

var _ contract.Runner = (*service)(nil)

// Run 由根 RunGroup 托管一个固定 worker，并由该 worker 独占唯一 debounce timer。
func (s *service) Run(ctx context.Context) error {
	if s == nil || s.skillsChangedWake == nil || s.skillsChangedDebounceWindow <= 0 {
		return fmt.Errorf("skill debounce runner is not configured")
	}
	timer := time.NewTimer(s.skillsChangedDebounceWindow)
	stopSkillsChangedTimer(timer)
	defer stopSkillsChangedTimer(timer)
	var timerC <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			s.stopSkillsChangedRunner()
			return nil
		case <-s.skillsChangedWake:
			resetSkillsChangedTimer(timer, s.skillsChangedDebounceWindow)
			timerC = timer.C
		case <-timerC:
			timerC = nil
			s.flushSkillsChanged()
		}
	}
}

func (s *service) stopSkillsChangedRunner() {
	s.skillsChangedMu.Lock()
	s.skillsChangedStopped = true
	s.skillsChangedMu.Unlock()
	s.flushSkillsChanged()
}

func resetSkillsChangedTimer(timer *time.Timer, window time.Duration) {
	stopSkillsChangedTimer(timer)
	timer.Reset(window)
}

func stopSkillsChangedTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

// flushSkillsChanged 取出当前窗口内的全部事件并按位置顺序发送。
func (s *service) flushSkillsChanged() {
	s.skillsChangedMu.Lock()
	queue := s.skillsChangedQueue
	s.skillsChangedQueue = nil
	next := s.skillsChangedNext
	s.skillsChangedNext = uidto.SkillsChanged{}
	emit := s.emitSkillsChanged
	s.skillsChangedMu.Unlock()

	if emit == nil {
		return
	}
	for _, ev := range queue {
		emit(ev)
	}
	if next.Count > 0 {
		emit(next)
	}
}

// skillsChangedLocation 为项目级变更计算 repo fingerprint 和 cwd 相对路径。
// 非项目 scope 不携带位置，避免 personal 事件被错误绑定到当前工作区。
func (s *service) skillsChangedLocation(ctx context.Context, scope string) (string, string) {
	if scope != skillScopeProject {
		return "", ""
	}
	cwd := cwdFromContext(ctx)
	projectRoot := s.projectRootForCWD(cwd)
	fp := RepoFingerprint(projectRoot)
	if fp == "" {
		return "", ""
	}
	canonicalRoot, rootErr := canonicalProjectPath(projectRoot)
	canonicalCWD, cwdErr := canonicalProjectPath(cwd)
	if rootErr != nil || cwdErr != nil {
		return fp, "."
	}
	rel, err := filepath.Rel(canonicalRoot, canonicalCWD)
	if err != nil || rel == "" || rel == "." || strings.HasPrefix(rel, "..") {
		return fp, "."
	}
	return fp, rel
}

// normalizeSkillsChanged 清理事件字段，并按 scope 移除不适用的 personal 或项目定位元数据。
func normalizeSkillsChanged(next uidto.SkillsChanged) uidto.SkillsChanged {
	next.SkillsDir = strings.TrimSpace(next.SkillsDir)
	next.Name = strings.TrimSpace(next.Name)
	next.Scope = strings.TrimSpace(next.Scope)
	next.PersonalType = strings.TrimSpace(next.PersonalType)
	next.RepoFingerprint = strings.TrimSpace(next.RepoFingerprint)
	next.RelativePath = strings.TrimSpace(next.RelativePath)
	next.Cwd = ""
	if next.Scope != skillScopeProject {
		next.RepoFingerprint = ""
		next.RelativePath = ""
	}
	if next.Scope != skillScopePersonal {
		next.PersonalType = ""
	}
	next.Action = normalizeSkillsChangedAction(next.Action)
	next.Actions = appendUniqueSkillsChangedActions(nil, next.Actions...)
	if next.Action != "" {
		next.Actions = appendUniqueSkillsChangedActions(next.Actions, next.Action)
	}
	return syncSkillsChangedActionSummary(next)
}

// normalizeSkillsChangedAction 收敛常见动作名称，方便前端按 import/delete/write 分类刷新。
func normalizeSkillsChangedAction(action string) string {
	action = strings.TrimSpace(action)
	switch {
	case action == "":
		return ""
	case strings.Contains(action, "import"):
		return "import"
	case strings.Contains(action, "delete"):
		return "delete"
	case strings.Contains(action, "write"):
		return "write"
	default:
		return action
	}
}

// mergeSkillsChanged 合并同一位置的多个 skill 变更事件。
func mergeSkillsChanged(current, next uidto.SkillsChanged) uidto.SkillsChanged {
	if current.Count == 0 {
		return next
	}
	// scheduleSkillsChanged 会把跨 scope/cwd 事件拆成多个 payload。
	// 这里保留兜底分支给旧调用方，避免把不同位置强行合并。
	if !skillsChangedMergeable(current, next) {
		return next
	}
	current = mergeSkillsChangedMetadata(current, next)
	current.Actions = appendUniqueSkillsChangedActions(current.Actions, next.Actions...)
	return syncSkillsChangedActionSummary(current)
}

// skillsChangedMergeable 判断两个事件是否可合并到同一 UI payload。
func skillsChangedMergeable(current, next uidto.SkillsChanged) bool {
	return current.Scope == next.Scope &&
		current.PersonalType == next.PersonalType &&
		current.RepoFingerprint == next.RepoFingerprint &&
		current.RelativePath == next.RelativePath
}

// mergeSkillsChangedMetadata 合并时间戳、目录和名称；不同名称合并后清空 Name 表示批量变更。
func mergeSkillsChangedMetadata(current, next uidto.SkillsChanged) uidto.SkillsChanged {
	if next.Timestamp.After(current.Timestamp) {
		current.EventHeader = next.EventHeader
	}
	if next.SkillsDir != "" {
		current.SkillsDir = next.SkillsDir
	}
	if current.Name == "" || next.Name == "" || current.Name != next.Name {
		current.Name = ""
	}
	return current
}

// syncSkillsChangedActionSummary 同步旧 Action 字段和新 Actions 列表，保持前端兼容。
func syncSkillsChangedActionSummary(ev uidto.SkillsChanged) uidto.SkillsChanged {
	switch len(ev.Actions) {
	case 0:
		ev.Action = ""
	case 1:
		ev.Action = ev.Actions[0]
	default:
		ev.Action = ""
	}
	ev.Count = len(ev.Actions)
	return ev
}

// appendUniqueSkillsChangedActions 追加去重后的规范化动作名称。
func appendUniqueSkillsChangedActions(dst []string, actions ...string) []string {
	for _, action := range actions {
		action = normalizeSkillsChangedAction(action)
		if action == "" || containsSkillsChangedAction(dst, action) {
			continue
		}
		dst = append(dst, action)
	}
	return dst
}

// containsSkillsChangedAction 判断动作列表中是否已有目标动作。
func containsSkillsChangedAction(actions []string, target string) bool {
	return slices.Contains(actions, target)
}
