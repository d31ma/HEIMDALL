package lambda

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type executionRequest struct {
	Target    provider.Target
	Plan      provider.Plan
	Desired   spec.DeploySpec
	Operation provider.Operation
}
