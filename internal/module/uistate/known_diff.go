package uistate

import "context"

type knownDiffRevisionKey struct{}

func withKnownDiffRevision(ctx context.Context, revision int) context.Context {
	if revision <= 0 {
		return ctx
	}
	return context.WithValue(ctx, knownDiffRevisionKey{}, revision)
}

func knownDiffRevisionFromContext(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	value, _ := ctx.Value(knownDiffRevisionKey{}).(int)
	return value
}
