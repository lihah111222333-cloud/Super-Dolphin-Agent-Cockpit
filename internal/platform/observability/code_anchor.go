package observability

import "runtime"

func NewCodeAnchor(file string, function string, line int) CodeAnchor {
	return CodeAnchor{File: file, Function: function, Line: line}
}

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
