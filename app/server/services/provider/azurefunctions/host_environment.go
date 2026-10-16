package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/d31ma/heimdall/app/server/models/spec"
)

type hostEnvironment struct{ config targetConfig }

func (e hostEnvironment) build(service spec.Service) []*armappcontainers.EnvironmentVar {
	environment := []*armappcontainers.EnvironmentVar{{Name: to.Ptr((functionContract{}).extensionSetting()), Value: to.Ptr("~4")}, {Name: to.Ptr("AzureWebJobsStorage__accountName"), Value: to.Ptr(e.config.storageName)}, {Name: to.Ptr("AzureWebJobsStorage__credential"), Value: to.Ptr("managedidentity")}, {Name: to.Ptr("AzureWebJobsStorage__clientId"), Value: to.Ptr(e.config.clientID)}}
	return append(environment, e.application(service)...)
}
func (hostEnvironment) application(service spec.Service) []*armappcontainers.EnvironmentVar {
	environment := []*armappcontainers.EnvironmentVar{}
	for _, value := range service.Env {
		environment = append(environment, &armappcontainers.EnvironmentVar{Name: to.Ptr(value.Key), Value: to.Ptr(value.Value)})
	}
	return environment
}
