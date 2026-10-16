package provider

import "github.com/d31ma/heimdall/app/server/models/spec"

type DesiredValidator interface {
	ValidateDesired(Target, spec.DeploySpec) error
}
