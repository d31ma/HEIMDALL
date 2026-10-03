package provider

import "github.com/d31ma/heimdall/internal/spec"

type DesiredCheck struct {
	Adapter Provider
	Target  Target
	Spec    spec.DeploySpec
}

func (c DesiredCheck) Validate() error {
	validator, available := c.Adapter.(DesiredValidator)
	return c.validate(validator, available)
}
func (c DesiredCheck) validate(validator DesiredValidator, available bool) error {
	if !available {
		return Validate(c.Adapter.Capabilities(), c.Spec)
	}
	return validator.ValidateDesired(c.Target, c.Spec)
}
