package lambda

import (
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type executionRequest struct {
	Target    provider.Target
	Plan      provider.Plan
	Desired   spec.DeploySpec
	Operation provider.Operation
}
