package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type functionDefinition struct{ config targetConfig }

func (d functionDefinition) build(plan provider.Plan, service spec.Service) (nativeApp, error) {
	config := d.config
	hash, err := spec.HashService(service)
	if err != nil {
		return nativeApp{}, err
	}
	environment := []*armappcontainers.EnvironmentVar{
		{Name: to.Ptr("FUNCTIONS_EXTENSION_VERSION"), Value: to.Ptr("~4")},
		{Name: to.Ptr("AzureWebJobsStorage__accountName"), Value: to.Ptr(config.storageName)},
		{Name: to.Ptr("AzureWebJobsStorage__credential"), Value: to.Ptr("managedidentity")},
		{Name: to.Ptr("AzureWebJobsStorage__clientId"), Value: to.Ptr(config.clientID)},
	}
	for _, env := range service.Env {
		environment = append(environment, &armappcontainers.EnvironmentVar{Name: to.Ptr(env.Key), Value: to.Ptr(env.Value)})
	}
	return nativeApp{
		Kind:     "functionapp",
		Location: to.Ptr(config.region),
		Tags: map[string]*string{
			(resourceIdentity{}).tag("managed-by"): to.Ptr("heimdall"), (resourceIdentity{}).tag("provider"): to.Ptr("azurefunctions"),
			(resourceIdentity{}).tag("project"): to.Ptr(plan.App.Project), (resourceIdentity{}).tag("app"): to.Ptr(plan.App.App),
			(resourceIdentity{}).tag("service"): to.Ptr(service.Name), (resourceIdentity{}).tag("revision"): to.Ptr(plan.Revision),
			(resourceIdentity{}).tag("service-hash"): to.Ptr(hash), (resourceIdentity{}).tag("spec-hash"): to.Ptr(plan.SpecHash),
		},
		Identity: &armappcontainers.ManagedServiceIdentity{
			Type:                   to.Ptr(armappcontainers.ManagedServiceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armappcontainers.UserAssignedIdentity{config.identity: {}},
		},
		Properties: &nativeProperties{
			EnvironmentID: to.Ptr(config.environment),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Ingress:             &armappcontainers.Ingress{External: to.Ptr(false), TargetPort: to.Ptr(int32(80)), AllowInsecure: to.Ptr(false), Transport: to.Ptr(armappcontainers.IngressTransportMethodAuto)},
			},
			Template: &nativeTemplate{
				Containers: []*armappcontainers.Container{{Name: to.Ptr(service.Name), Image: to.Ptr(service.Image), Env: environment, Resources: &armappcontainers.ContainerResources{CPU: to.Ptr(0.5), Memory: to.Ptr("1Gi")}}},
				Scale:      &nativeScale{AllowScalingRuleOverride: to.Ptr(false), MinReplicas: to.Ptr(int32(0)), MaxReplicas: to.Ptr(int32(10))},
			},
		},
	}, nil
}
