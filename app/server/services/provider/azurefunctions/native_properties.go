package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type nativeProperties struct {
	Configuration           *armappcontainers.Configuration                 `json:"configuration,omitempty"`
	EnvironmentID           *string                                         `json:"environmentId,omitempty"`
	ManagedEnvironmentID    *string                                         `json:"managedEnvironmentId,omitempty"`
	Template                *nativeTemplate                                 `json:"template,omitempty"`
	WorkloadProfileName     *string                                         `json:"workloadProfileName,omitempty"`
	LatestReadyRevisionName *string                                         `json:"latestReadyRevisionName,omitempty"`
	LatestRevisionName      *string                                         `json:"latestRevisionName,omitempty"`
	ProvisioningState       *armappcontainers.ContainerAppProvisioningState `json:"provisioningState,omitempty"`
	RunningStatus           *armappcontainers.ContainerAppRunningStatus     `json:"runningStatus,omitempty"`
}
