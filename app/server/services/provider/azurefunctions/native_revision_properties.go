package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type nativeRevisionProperties struct {
	Active            *bool                                       `json:"active,omitempty"`
	HealthState       *armappcontainers.RevisionHealthState       `json:"healthState,omitempty"`
	ProvisioningState *armappcontainers.RevisionProvisioningState `json:"provisioningState,omitempty"`
	RunningState      *armappcontainers.RevisionRunningState      `json:"runningState,omitempty"`
	Replicas          *int32                                      `json:"replicas,omitempty"`
	Template          *nativeTemplate                             `json:"template,omitempty"`
}
