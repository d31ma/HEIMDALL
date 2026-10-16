package azurefunctions

import (
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
)

type containerComparison struct{}

func (c containerComparison) matches(actual, expected *armappcontainers.Container) bool {
	live := c.value(actual)
	desired := c.value(expected)
	return (comparisonChecks{}).all([]bool{
		c.present(actual, expected),
		(optionalValue[string]{}).read(live.Name) == (optionalValue[string]{}).read(desired.Name),
		(optionalValue[string]{}).read(live.Image) == (optionalValue[string]{}).read(desired.Image),
		len(live.Command) == 0, len(live.Args) == 0, len(live.Probes) == 0, len(live.VolumeMounts) == 0,
		c.resources(live.Resources, desired.Resources),
		reflect.DeepEqual((environmentValues{}).read(live.Env), (environmentValues{}).read(desired.Env)),
	})
}
func (containerComparison) present(actual, expected *armappcontainers.Container) bool {
	return actual != nil && expected != nil
}
func (containerComparison) value(container *armappcontainers.Container) armappcontainers.Container {
	if container == nil {
		return armappcontainers.Container{}
	}
	return *container
}
func (c containerComparison) resources(actual, expected *armappcontainers.ContainerResources) bool {
	live := c.resourceValue(actual)
	desired := c.resourceValue(expected)
	return (comparisonChecks{}).all([]bool{c.resourcesPresent(actual, expected), (optionalValue[float64]{}).read(live.CPU) == (optionalValue[float64]{}).read(desired.CPU), (optionalValue[string]{}).read(live.Memory) == (optionalValue[string]{}).read(desired.Memory)})
}
func (containerComparison) resourcesPresent(actual, expected *armappcontainers.ContainerResources) bool {
	return actual != nil && expected != nil
}
func (containerComparison) resourceValue(resources *armappcontainers.ContainerResources) armappcontainers.ContainerResources {
	if resources == nil {
		return armappcontainers.ContainerResources{}
	}
	return *resources
}
