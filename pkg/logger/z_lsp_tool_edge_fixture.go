package logger

import "fmt"

type edgeDoer interface {
	Do() string
}

type edgeType struct{}

func (edgeType) Do() string {
	return "edge"
}

func edgeJoin(left string, right string) string {
	return fmt.Sprintf("%s-%s", left, right)
}

func edgeRun() string {
	var d edgeDoer = edgeType{}
	_ = d.Do()
	return edgeJoin("a", "b")
}
