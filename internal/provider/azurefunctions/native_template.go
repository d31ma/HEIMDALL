package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type nativeTemplate struct {
	Containers     []*armappcontainers.Container     `json:"containers,omitempty"`
	InitContainers []*armappcontainers.InitContainer `json:"initContainers,omitempty"`
	RevisionSuffix *string                           `json:"revisionSuffix,omitempty"`
	Scale          *nativeScale                      `json:"scale,omitempty"`
	Volumes        []*armappcontainers.Volume        `json:"volumes,omitempty"`
}
