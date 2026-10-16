package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type ingressComparison struct{}

func (c ingressComparison) matches(ingress *armappcontainers.Ingress) bool {
	value := c.value(ingress)
	return (comparisonChecks{}).all([]bool{
		ingress != nil,
		!(optionalValue[bool]{}).read(value.External),
		!(optionalValue[bool]{}).read(value.AllowInsecure),
		(optionalValue[int32]{}).read(value.TargetPort) == (functionContract{}).targetPort(),
		(optionalValue[armappcontainers.IngressTransportMethod]{}).read(value.Transport) == armappcontainers.IngressTransportMethodAuto,
		len(value.AdditionalPortMappings) == 0,
	})
}
func (ingressComparison) value(ingress *armappcontainers.Ingress) armappcontainers.Ingress {
	if ingress == nil {
		return armappcontainers.Ingress{}
	}
	return *ingress
}
