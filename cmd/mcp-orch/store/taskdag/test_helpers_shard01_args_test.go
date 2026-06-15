//go:build legacy_pg_fake

package taskdag

import (
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

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

func fakeTaskDAGInt8Arg(index int, name string) fakeTaskDAGArgSpec {
	return fakeTaskDAGArgSpec{
		index: index,
		name:  name,
		valid: func(value any) bool {
			_, err := fakeInt8Arg([]any{value}, 0, name)
			return err == nil
		},
	}
}

func fakeTaskDAGTextArg(index int, name string) fakeTaskDAGArgSpec {
	return fakeTaskDAGArgSpec{
		index: index,
		name:  name,
		valid: func(value any) bool {
			_, err := fakeTextArg([]any{value}, 0, name)
			return err == nil
		},
	}
}

func fakeInt8Arg(args []any, index int, name string) (int64, error) {
	switch value := args[index].(type) {
	case int64:
		return value, nil
	case sqlc.Int8:
		if !value.Valid {
			return 0, fmt.Errorf("%s arg invalid", name)
		}
		return value.Int64, nil
	default:
		return 0, fmt.Errorf("%s arg = %T", name, args[index])
	}
}

func fakeTextArg(args []any, index int, name string) (string, error) {
	switch value := args[index].(type) {
	case string:
		return value, nil
	case sqlc.Text:
		if !value.Valid {
			return "", fmt.Errorf("%s arg invalid", name)
		}
		return value.String, nil
	default:
		return "", fmt.Errorf("%s arg = %T", name, args[index])
	}
}
