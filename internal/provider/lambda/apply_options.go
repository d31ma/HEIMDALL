package lambda

import (
	"context"
	"github.com/d31ma/heimdall/internal/spec"
)

type ApplyOptions struct {
	Spec  spec.DeploySpec
	Prune bool
}

func (o ApplyOptions) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, applyKey{}, o)
}
