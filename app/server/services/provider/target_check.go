package provider

type TargetCheck struct {
	Adapter Provider
	Target  Target
}

func (c TargetCheck) Definition() error {
	validator, available := c.Adapter.(TargetValidator)
	return c.validate(validator, available)
}
func (c TargetCheck) validate(validator TargetValidator, available bool) error {
	if !available {
		return nil
	}
	return validator.ValidateTargetDefinition(c.Target)
}
