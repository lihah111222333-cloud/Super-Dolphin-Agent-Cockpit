package observability

import "runtime"

// NewCodeAnchor 构造稳定的源码锚点，调用方负责传入已知的文件、函数和行号。
func NewCodeAnchor(file string, function string, line int) CodeAnchor {
	return CodeAnchor{File: file, Function: function, Line: line}
}

// CodeAnchorFromCaller 从调用栈捕获源码锚点，skip 会在本函数之上再额外跳过指定帧数。
func CodeAnchorFromCaller(skip int) CodeAnchor {
	pc, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return CodeAnchor{}
	}
	function := ""
	if fn := runtime.FuncForPC(pc); fn != nil {
		function = fn.Name()
	}
	return NewCodeAnchor(file, function, line)
}
