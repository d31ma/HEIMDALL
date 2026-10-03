package provider

import "github.com/d31ma/heimdall/internal/spec"

type DesiredValidator interface {
	ValidateDesired(Target, spec.DeploySpec) error
}
