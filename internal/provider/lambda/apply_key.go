package lambda

import "context"

type applyKey struct{}

func WithApply(ctx context.Context, options ApplyOptions) context.Context {
	return options.Context(ctx)
}
