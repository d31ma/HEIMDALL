package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type clientFactory struct{ provider *Provider }

func (f clientFactory) create(config targetConfig) (*armClient, error) {
	if f.provider.ClientOptions != nil && f.provider.ClientOptions.APIVersion != "" && f.provider.ClientOptions.APIVersion != "2026-07-01" {
		return nil, (&AdapterError{Code: "HD0471", Message: "native Functions ARM API version is pinned to 2026-07-01"})
	}
	credential := f.provider.Credential
	if credential == nil {
		ambient, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, (&AdapterError{Code: "HD0471", Message: "initialize ambient Azure credential", Cause: err})
		}
		credential = ambient
	}
	options := f.provider.ClientOptions.Clone()
	options.DisableRPRegistration = true
	sdk, err := arm.NewClient("github.com/d31ma/heimdall/internal/provider/azurefunctions", "v0.1.0", credential, options)
	if err != nil {
		return nil, err
	}
	return &armClient{sdk: sdk, config: config}, nil
}
