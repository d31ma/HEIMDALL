package reconcile

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type convergenceInput struct {
	resolved resolved
	want     spec.DeploySpec
	plan     provider.Plan
}
