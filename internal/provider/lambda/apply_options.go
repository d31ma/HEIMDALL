package lambda

import (
	"context"
	"github.com/d31ma/heimdall/internal/spec"
)

type ApplyOptions struct {
	Spec  spec.DeploySpec
	Prune bool
}

func (o ApplyOptions) WithContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, applyKey{}, o)
}
