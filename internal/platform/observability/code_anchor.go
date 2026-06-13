package observability

import "runtime"

// NewCodeAnchor 创建代码锚点。
func NewCodeAnchor(file string, function string, line int) CodeAnchor {
	return CodeAnchor{File: file, Function: function, Line: line}
}

// CodeAnchorFromCaller 从caller处理代码锚点。
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
