package azurefunctions

import "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"

type nativeApp struct {
	ID         *string                                  `json:"id,omitempty"`
	Name       *string                                  `json:"name,omitempty"`
	Type       *string                                  `json:"type,omitempty"`
	Kind       string                                   `json:"kind"`
	Location   *string                                  `json:"location,omitempty"`
	Tags       map[string]*string                       `json:"tags,omitempty"`
	Identity   *armappcontainers.ManagedServiceIdentity `json:"identity,omitempty"`
	Properties *nativeProperties                        `json:"properties,omitempty"`
}

func (app *nativeApp) tagValue(name string) string {
	return (optionalValue[string]{}).read(app.Tags[(resourceIdentity{}).tag(name)])
}
