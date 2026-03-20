package rpc

import "context"

type cwdKey struct{}

func WithCWD(ctx context.Context, cwd string) context.Context {
	return context.WithValue(ctx, cwdKey{}, cwd)
}

func CWDFrom(ctx context.Context) string {
	value, _ := ctx.Value(cwdKey{}).(string)
	return value
}
