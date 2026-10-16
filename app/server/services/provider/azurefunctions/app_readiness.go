package azurefunctions

import (
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type appReadiness struct {
	missing       bool
	kind          string
	hasProperties bool
	running       string
	provisioning  string
	latest        string
	readyRevision string
}

func (r appReadiness) health(app *nativeApp) provider.Health {
	r.readApp(app)
	return r.classify()
}
func (r *appReadiness) readApp(app *nativeApp) {
	r.missing = app == nil
	value := (appValues{}).read(app)
	r.kind = value.Kind
	r.hasProperties = value.Properties != nil
	properties := (appValues{}).properties(value.Properties)
	r.running = string((optionalValue[armappcontainers.ContainerAppRunningStatus]{}).read(properties.RunningStatus))
	r.provisioning = string((optionalValue[armappcontainers.ContainerAppProvisioningState]{}).read(properties.ProvisioningState))
	r.latest = (optionalValue[string]{}).read(properties.LatestRevisionName)
	r.readyRevision = (optionalValue[string]{}).read(properties.LatestReadyRevisionName)
}
func (r appReadiness) classify() provider.Health {
	switch {
	case r.missing:
		return provider.Missing
	case r.kind != (functionContract{}).resourceKind() || !r.hasProperties:
		return provider.Degraded
	case strings.EqualFold(r.running, r.failed()):
		return provider.Degraded
	case strings.EqualFold(r.running, r.stopped()):
		return provider.Suspended
	case strings.EqualFold(r.provisioning, r.failed()) || strings.EqualFold(r.provisioning, r.canceled()):
		return provider.Degraded
	case !strings.EqualFold(r.provisioning, r.succeeded()) || r.latest == "" || r.latest != r.readyRevision:
		return provider.Progressing
	}
	return provider.Healthy
}
func (appReadiness) failed() string    { return "Failed" }
func (appReadiness) stopped() string   { return "Stopped" }
func (appReadiness) canceled() string  { return "Canceled" }
func (appReadiness) succeeded() string { return "Succeeded" }
