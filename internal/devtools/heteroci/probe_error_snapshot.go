package heteroci

import (
	"reflect"
	"slices"
)

const maxProbeExternalErrorNodes = 64

// probeExternalErrorSnapshot 是一次性读取外部错误树得到的安全分类与根因快照。
type probeExternalErrorSnapshot struct {
	cause           error
	onlyUnavailable bool
}

// probeOpaqueCause 仅保留可比较错误节点的身份，不暴露外部 Unwrap 实现。
type probeOpaqueCause struct {
	matches []error
}

// Error 返回不读取外部错误文本的稳定安全文案。
func (*probeOpaqueCause) Error() string {
	return "probe transport failed"
}

// Is 只比较采集时冻结的错误身份，不调用外部 Error、Is、As 或 Unwrap。
func (cause *probeOpaqueCause) Is(target error) bool {
	if cause == nil || target == nil || !reflect.ValueOf(target).Comparable() {
		return false
	}
	return slices.Contains(cause.matches, target)
}

// probeExternalErrorWalker 以共享总节点预算采集错误身份并判定 unavailable 单链。
type probeExternalErrorWalker struct {
	remaining int
	visiting  map[error]struct{}
	matches   []error
}

// snapshotProbeExternalError 只遍历外部错误树一次，并且不把原始树放入返回错误链。
func snapshotProbeExternalError(err error) probeExternalErrorSnapshot {
	if err == nil {
		return probeExternalErrorSnapshot{}
	}
	walker := probeExternalErrorWalker{
		remaining: maxProbeExternalErrorNodes,
		visiting:  make(map[error]struct{}),
	}
	onlyUnavailable, safe := walker.capture(err)
	if !safe {
		walker.matches = nil
		onlyUnavailable = false
	}
	return probeExternalErrorSnapshot{
		cause:           &probeOpaqueCause{matches: append([]error(nil), walker.matches...)},
		onlyUnavailable: onlyUnavailable,
	}
}

// capture 递归采集有限错误树；panic、循环和预算耗尽都使整棵快照失效。
func (walker *probeExternalErrorWalker) capture(err error) (onlyUnavailable bool, safe bool) {
	defer func() {
		if recover() != nil {
			onlyUnavailable = false
			safe = false
		}
	}()
	comparable, entered := walker.enter(err)
	if !entered {
		return false, false
	}
	if comparable {
		defer delete(walker.visiting, err)
	}

	if comparable && err == ErrProbeTransportUnavailable {
		return true, true
	}
	return walker.captureUnwrapped(err)
}

// enter 消耗一个节点预算，并为可比较节点建立递归栈身份。
func (walker *probeExternalErrorWalker) enter(err error) (comparable bool, entered bool) {
	if err == nil || isNilProbeValue(err) {
		return false, false
	}
	if walker.remaining <= 0 {
		return false, false
	}
	walker.remaining--
	comparable = reflect.ValueOf(err).Comparable()
	if !comparable {
		return false, true
	}
	if _, exists := walker.visiting[err]; exists {
		return true, false
	}
	walker.visiting[err] = struct{}{}
	walker.matches = append(walker.matches, err)
	return true, true
}

// captureUnwrapped 按标准单因果或多因果接口继续快照，不调用 errors.Is 或 errors.As。
func (walker *probeExternalErrorWalker) captureUnwrapped(err error) (onlyUnavailable bool, safe bool) {
	switch unwrapper := err.(type) {
	case interface{ Unwrap() []error }:
		return walker.captureJoined(unwrapper.Unwrap())
	case interface{ Unwrap() error }:
		return walker.capture(unwrapper.Unwrap())
	default:
		return false, true
	}
}

// captureJoined 仅把恰含一个根因的多因果外壳视为 unavailable 单链。
func (walker *probeExternalErrorWalker) captureJoined(children []error) (onlyUnavailable bool, safe bool) {
	if len(children) == 0 {
		return false, true
	}
	onlyUnavailable = len(children) == 1
	for _, child := range children {
		childUnavailable, childSafe := walker.capture(child)
		if !childSafe {
			return false, false
		}
		onlyUnavailable = onlyUnavailable && childUnavailable
	}
	return onlyUnavailable, true
}
