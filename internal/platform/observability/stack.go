package observability

import (
	"encoding/json"
	"runtime"
	"strings"
)

func CaptureStackForStatus(cfg Config, status Status) []StackFrame {
	if !cfg.CaptureStackFor(status) {
		return nil
	}
	return CaptureStack(cfg)
}

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

func keepFrame(frame runtime.Frame) bool {
	if strings.Contains(frame.Function, "runtime.") {
		return false
	}
	if strings.Contains(frame.Function, "internal/platform/observability.CaptureStack") {
		return false
	}
	return true
}

func trimStackBytes(frames []StackFrame, maxBytes int) []StackFrame {
	for len(frames) > 0 && stackJSONSize(frames) > maxBytes {
		frames = frames[:len(frames)-1]
	}
	return frames
}

func stackJSONSize(frames []StackFrame) int {
	data, err := MarshalStackJSON(frames)
	if err != nil {
		return len(frames) * 1024
	}
	return len(data)
}

func MarshalStackJSON(frames []StackFrame) ([]byte, error) {
	return json.Marshal(frames)
}
