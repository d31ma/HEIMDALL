package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type templateComparison struct{}

func (c templateComparison) matches(template, expected *nativeTemplate) bool {
	actual := c.value(template)
	return (comparisonChecks{}).all([]bool{
		c.present(template, expected),
		len(actual.InitContainers) == 0,
		len(actual.Volumes) == 0,
		(scaleComparison{}).matches(actual.Scale),
		(containerComparison{}).matches(c.container(template), c.container(expected)),
	})
}
func (templateComparison) present(actual, expected *nativeTemplate) bool {
	return actual != nil && expected != nil
}
func (templateComparison) value(template *nativeTemplate) nativeTemplate {
	if template == nil {
		return nativeTemplate{}
	}
	return *template
}
func (templateComparison) container(template *nativeTemplate) *armappcontainers.Container {
	if template == nil || len(template.Containers) != (functionContract{}).containerCount() {
		return nil
	}
	return template.Containers[0]
}
func (templateComparison) environmentValues(environment []*armappcontainers.EnvironmentVar) map[string]string {
	return (environmentValues{}).read(environment)
}
