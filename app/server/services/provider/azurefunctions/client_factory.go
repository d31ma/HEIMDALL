package azurefunctions

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type clientFactory struct {
	provider   *Provider
	config     targetConfig
	credential azcore.TokenCredential
	sdk        *arm.Client
}

func (f clientFactory) create(config targetConfig) (*armClient, error) {
	f.config = config
	err := (sequence.Steps{Actions: []func() error{f.validateVersion, f.resolveCredential, f.createSDK}}).Run()
	return f.result(err)
}
func (f *clientFactory) validateVersion() error {
	if f.provider.ClientOptions != nil && f.provider.ClientOptions.APIVersion != "" && f.provider.ClientOptions.APIVersion != (functionContract{}).apiVersion() {
		return &AdapterError{Code: (functionContract{}).clientCode(), Message: "native Functions ARM API version is pinned to 2026-07-01"}
	}
	return nil
}
func (f *clientFactory) resolveCredential() error {
	f.credential = f.provider.Credential
	err := f.ambientCredential()
	return f.credentialError(err)
}
func (f *clientFactory) ambientCredential() error {
	if f.credential != nil {
		return nil
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	f.credential = credential
	return err
}
func (f *clientFactory) createSDK() error {
	options := f.provider.ClientOptions.Clone()
	options.DisableRPRegistration = true
	sdk, err := arm.NewClient("github.com/d31ma/heimdall/app/server/services/provider/azurefunctions", "v0.1.0", f.credential, options)
	f.sdk = sdk
	return err
}
func (f clientFactory) result(err error) (*armClient, error) {
	if err != nil {
		return nil, err
	}
	return &armClient{sdk: f.sdk, config: f.config}, nil
}

func (clientFactory) credentialError(err error) error {
	if err != nil {
		return &AdapterError{Code: (functionContract{}).clientCode(), Message: "initialize ambient Azure credential", Cause: err}
	}
	return nil
}
