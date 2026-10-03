package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type configurationComparison struct{}

func (c configurationComparison) matches(configuration *armappcontainers.Configuration) bool {
	value := c.value(configuration)
	return (comparisonChecks{}).all([]bool{
		configuration != nil,
		(optionalValue[armappcontainers.ActiveRevisionsMode]{}).read(value.ActiveRevisionsMode) == armappcontainers.ActiveRevisionsModeSingle,
		len(value.Secrets) == 0,
		len(value.Registries) == 0,
		(ingressComparison{}).matches(value.Ingress),
	})
}
func (configurationComparison) value(configuration *armappcontainers.Configuration) armappcontainers.Configuration {
	if configuration == nil {
		return armappcontainers.Configuration{}
	}
	return *configuration
}
