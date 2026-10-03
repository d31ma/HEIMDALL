package lambda

import "github.com/d31ma/heimdall/internal/provider"

type capabilitySet struct{}

func (c capabilitySet) Build() provider.Capabilities {
	support := map[provider.Feature]provider.Support{}
	caveats := map[provider.Feature]string{}
	c.defaults(support, caveats)

	support[provider.FeatureMultiService] = provider.Full
	support[provider.FeatureFunction] = provider.Partial
	caveats[provider.FeatureFunction] = "default Lambda only; immutable same-region ECR Runtime API image, direct JSON invocation and existing execution role required"
	support[provider.FeatureResources] = provider.Partial
	caveats[provider.FeatureResources] = "memory 128-10240 MiB; CPU is determined by Lambda; independent CPU requests are rejected"
	support[provider.FeatureSecretRef] = provider.Partial
	caveats[provider.FeatureSecretRef] = "environment references resolve only during apply; plan compares keys, references and literal values without reading secrets"
	support[provider.FeatureScaleToZero] = provider.Partial
	caveats[provider.FeatureScaleToZero] = "idle execution environments are native Lambda behavior; container replica counts and provisioned concurrency are not managed"
	return provider.Capabilities{Provider: (&Provider{}).Name(), Support: support, Caveats: caveats}
}

func (capabilitySet) defaults(support map[provider.Feature]provider.Support, caveats map[provider.Feature]string) {
	for _, feature := range provider.Features {
		support[feature] = provider.Rejected
		caveats[feature] = "default Lambda functions do not implement this container lifecycle feature"
	}
}
