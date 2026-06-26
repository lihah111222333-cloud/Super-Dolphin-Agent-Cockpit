package observability

import (
	"encoding/json"
	"runtime"
	"strings"
)

// CaptureStackForStatus 只在配置允许当前状态时采集调用栈。
func CaptureStackForStatus(cfg Config, status Status) []StackFrame {
	if !cfg.CaptureStackFor(status) {
		return nil
	}
	return CaptureStack(cfg)
}

// CaptureStack 采集当前调用栈，并过滤 runtime 与采集函数自身帧。
func CaptureStack(cfg Config) []StackFrame {
	pcs := make([]uintptr, cfg.StackMaxFrames+16)
	count := runtime.Callers(2, pcs)
	frames := runtime.CallersFrames(pcs[:count])
	out := make([]StackFrame, 0, cfg.StackMaxFrames)
	for len(out) < cfg.StackMaxFrames {
		frame, more := frames.Next()
		if keepFrame(frame) {
			out = append(out, StackFrame{File: frame.File, Function: frame.Function, Line: frame.Line})
		}
		if !more {
			break
		}
	}
	return trimStackBytes(out, cfg.StackMaxBytes)
}

// keepFrame 判断栈帧是否应暴露给 trace 诊断。
func keepFrame(frame runtime.Frame) bool {
	if strings.Contains(frame.Function, "runtime.") {
		return false
	}
	if strings.Contains(frame.Function, "internal/platform/observability.CaptureStack") {
		return false
	}
	return true
}

// trimStackBytes 从末尾裁剪栈帧，直到 JSON 编码大小不超过配置上限。
func trimStackBytes(frames []StackFrame, maxBytes int) []StackFrame {
	for len(frames) > 0 && stackJSONSize(frames) > maxBytes {
		frames = frames[:len(frames)-1]
	}
	return frames
}

// stackJSONSize 返回栈帧 JSON 编码大小，编码失败时用保守估算值。
func stackJSONSize(frames []StackFrame) int {
	data, err := MarshalStackJSON(frames)
	if err != nil {
		return len(frames) * 1024
	}
	return len(data)
}

// MarshalStackJSON 编码调用栈帧。
func MarshalStackJSON(frames []StackFrame) ([]byte, error) {
	return json.Marshal(frames)
}
