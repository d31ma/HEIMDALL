package azurefunctions

import "github.com/d31ma/heimdall/app/server/services/provider"

type capabilitySet struct{}

func (capabilitySet) build() provider.Capabilities {
	capabilities := provider.Capabilities{Provider: (functionContract{}).providerName(), Support: map[provider.Feature]provider.Support{}, Caveats: map[provider.Feature]string{}}
	for _, feature := range provider.Features {
		capabilities.Support[feature] = provider.Rejected
		capabilities.Caveats[feature] = "the initial Functions v4 HTTP image contract does not express this container directive"
	}
	capabilities.Support[provider.FeatureMultiService] = provider.Full
	capabilities.Support[provider.FeatureFunction] = provider.Partial
	capabilities.Support[provider.FeatureScaleToZero] = provider.Full
	capabilities.Caveats[provider.FeatureFunction] = "native Container Apps Functions v4 HTTP images only; declared compatibility needs independent image runtime qualification"
	capabilities.Caveats[provider.FeatureSecretRef] = "environment secret references are deferred; host storage uses an existing managed identity"
	capabilities.Caveats[provider.FeatureResources] = "the initial hosting contract uses 0.5 CPU and 1Gi memory"
	return capabilities
}
