package taskdag

import "fmt"

type fakeTaskDAGArgSpec struct {
	index int
	name  string
	valid func(any) bool
}

func requireFakeTaskDAGArgs(args []any, want int, label string, specs ...fakeTaskDAGArgSpec) error {
	if len(args) != want {
		return fmt.Errorf("%s args len = %d, want %d", label, len(args), want)
	}
	for _, spec := range specs {
		if !spec.valid(args[spec.index]) {
			return fmt.Errorf("%s arg = %T", spec.name, args[spec.index])
		}
	}
	return nil
}

func fakeTaskDAGTypedArg[T any](index int, name string) fakeTaskDAGArgSpec {
	return fakeTaskDAGArgSpec{
		index: index,
		name:  name,
		valid: func(value any) bool {
			_, ok := value.(T)
			return ok
		},
	}
}
