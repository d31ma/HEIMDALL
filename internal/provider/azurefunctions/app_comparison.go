package azurefunctions

import (
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/internal/provider"
)

type appComparison struct{}

func (appComparison) ready(app *nativeApp) provider.Health {
	if app == nil {
		return provider.Missing
	}
	if app.Kind != "functionapp" || app.Properties == nil {
		return provider.Degraded
	}
	properties := app.Properties
	if strings.EqualFold(string((optionalValue[armappcontainers.ContainerAppRunningStatus]{}).read(properties.RunningStatus)), "Failed") {
		return provider.Degraded
	}
	if strings.EqualFold(string((optionalValue[armappcontainers.ContainerAppRunningStatus]{}).read(properties.RunningStatus)), "Stopped") {
		return provider.Suspended
	}
	if strings.EqualFold(string((optionalValue[armappcontainers.ContainerAppProvisioningState]{}).read(properties.ProvisioningState)), "Failed") || strings.EqualFold(string((optionalValue[armappcontainers.ContainerAppProvisioningState]{}).read(properties.ProvisioningState)), "Canceled") {
		return provider.Degraded
	}
	if !strings.EqualFold(string((optionalValue[armappcontainers.ContainerAppProvisioningState]{}).read(properties.ProvisioningState)), "Succeeded") || (optionalValue[string]{}).read(properties.LatestRevisionName) == "" || (optionalValue[string]{}).read(properties.LatestRevisionName) != (optionalValue[string]{}).read(properties.LatestReadyRevisionName) {
		return provider.Progressing
	}
	return provider.Healthy
}

func (appComparison) matches(live *nativeApp, desired nativeApp) bool {
	if live == nil || live.Kind != desired.Kind || !strings.EqualFold((optionalValue[string]{}).read(live.Location), (optionalValue[string]{}).read(desired.Location)) || live.Properties == nil || live.Identity == nil || desired.Identity == nil {
		return false
	}
	for key, expected := range desired.Tags {
		if (optionalValue[string]{}).read(live.Tags[key]) != (optionalValue[string]{}).read(expected) {
			return false
		}
	}
	if (optionalValue[armappcontainers.ManagedServiceIdentityType]{}).read(live.Identity.Type) != (optionalValue[armappcontainers.ManagedServiceIdentityType]{}).read(desired.Identity.Type) || len(live.Identity.UserAssignedIdentities) != len(desired.Identity.UserAssignedIdentities) {
		return false
	}
	for expected := range desired.Identity.UserAssignedIdentities {
		found := false
		for actual := range live.Identity.UserAssignedIdentities {
			if strings.EqualFold(expected, actual) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	actual, expected := live.Properties, desired.Properties
	if !strings.EqualFold((optionalValue[string]{}).read(actual.EnvironmentID), (optionalValue[string]{}).read(expected.EnvironmentID)) || actual.Configuration == nil || actual.Template == nil {
		return false
	}
	configuration := actual.Configuration
	if (optionalValue[armappcontainers.ActiveRevisionsMode]{}).read(configuration.ActiveRevisionsMode) != armappcontainers.ActiveRevisionsModeSingle || configuration.Ingress == nil || len(configuration.Secrets) != 0 || len(configuration.Registries) != 0 {
		return false
	}
	ingress := configuration.Ingress
	if (optionalValue[bool]{}).read(ingress.External) || (optionalValue[bool]{}).read(ingress.AllowInsecure) || (optionalValue[int32]{}).read(ingress.TargetPort) != 80 || (optionalValue[armappcontainers.IngressTransportMethod]{}).read(ingress.Transport) != armappcontainers.IngressTransportMethodAuto || len(ingress.AdditionalPortMappings) != 0 {
		return false
	}
	return (templateComparison{}).matches(actual.Template, expected.Template)
}
