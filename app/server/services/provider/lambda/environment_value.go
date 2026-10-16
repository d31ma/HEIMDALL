package lambda

import (
	"context"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type environmentValue struct {
	environment *environment
	setting     spec.EnvVar
	value       string
}

func (v *environmentValue) Resolve(ctx context.Context) error {
	v.value = v.setting.Value
	reference := []func() error{v.available, func() error { return v.read(ctx) }}
	steps := v.referenceSteps(reference)
	steps = append(steps, v.bound, v.store)
	return (sequence.Steps{Actions: steps}).Run()
}
func (v *environmentValue) referenceSteps(reference []func() error) []func() error {
	if v.setting.Ref == "" {
		return nil
	}
	return reference
}
func (v *environmentValue) available() error {
	if v.environment.resolver == nil {
		return (Diagnostic{}).Format((Diagnostic{}).EnvironmentCode(), "function %s requires an environment secret resolver", v.environment.service.Name)
	}
	return nil
}
func (v *environmentValue) read(ctx context.Context) error {
	value, err := v.environment.resolver(ctx, v.setting.Ref)
	v.value = value
	if err != nil {
		return (Diagnostic{}).Format((Diagnostic{}).EnvironmentCode(), "function %s environment reference could not be resolved", v.environment.service.Name)
	}
	return nil
}
func (v *environmentValue) bound() error {
	v.environment.bytes += len(v.setting.Key) + len(v.value)
	if v.environment.bytes > (functionLimits{}).EnvironmentBytes() {
		return (Diagnostic{}).Format((Diagnostic{}).EnvironmentCode(), "Lambda environment exceeds 4096 bytes")
	}
	return nil
}
func (v *environmentValue) store() error {
	v.environment.variables[v.setting.Key] = v.value
	return nil
}
