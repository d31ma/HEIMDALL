package provider

import "context"

type Invoker interface {
	Invoke(context.Context, InvocationRequest) (Invocation, error)
}
