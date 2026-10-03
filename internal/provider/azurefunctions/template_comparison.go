package azurefunctions

import (
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
)

type templateComparison struct{}

func (templateComparison) matches(template, expected *nativeTemplate) bool {
	if template == nil || expected == nil || len(template.Containers) != 1 || template.Containers[0] == nil || len(expected.Containers) != 1 || expected.Containers[0] == nil || len(template.InitContainers) != 0 || len(template.Volumes) != 0 || template.Scale == nil {
		return false
	}
	if (optionalValue[int32]{}).read(template.Scale.MinReplicas) != 0 || (optionalValue[int32]{}).read(template.Scale.MaxReplicas) != 10 || (optionalValue[bool]{}).read(template.Scale.AllowScalingRuleOverride) {
		return false
	}
	container, want := template.Containers[0], expected.Containers[0]
	if (optionalValue[string]{}).read(container.Name) != (optionalValue[string]{}).read(want.Name) || (optionalValue[string]{}).read(container.Image) != (optionalValue[string]{}).read(want.Image) || len(container.Command) != 0 || len(container.Args) != 0 || len(container.Probes) != 0 || len(container.VolumeMounts) != 0 || container.Resources == nil || want.Resources == nil || (optionalValue[float64]{}).read(container.Resources.CPU) != (optionalValue[float64]{}).read(want.Resources.CPU) || (optionalValue[string]{}).read(container.Resources.Memory) != (optionalValue[string]{}).read(want.Resources.Memory) {
		return false
	}
	return reflect.DeepEqual((templateComparison{}).environmentValues(container.Env), (templateComparison{}).environmentValues(want.Env))
}

func (templateComparison) environmentValues(environment []*armappcontainers.EnvironmentVar) map[string]string {
	values := map[string]string{}
	for _, env := range environment {
		if env != nil {
			values[(optionalValue[string]{}).read(env.Name)] = (optionalValue[string]{}).read(env.Value) + "\x00" + (optionalValue[string]{}).read(env.SecretRef)
		}
	}
	return values
}
