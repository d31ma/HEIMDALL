package provider

type TargetValidator interface {
	ValidateTargetDefinition(Target) error
}
