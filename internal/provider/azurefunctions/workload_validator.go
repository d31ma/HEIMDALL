package azurefunctions

import (
	"regexp"
	"strings"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type workloadValidator struct{}

func (workloadValidator) validate(want spec.DeploySpec) error {
	if err := provider.Validate((capabilitySet{}).build(), want); err != nil {
		return err
	}
	rejections := []provider.Rejection{}
	image := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`)
	for _, service := range want.Services {
		detail := ""
		switch {
		case service.Function == nil:
			detail = "declare x-heimdall-function; ordinary container images are not Functions workloads"
		case service.Function.RuntimeContract != "azure-functions-v4" || service.Function.Invocation != "http":
			detail = "requires azure-functions-v4 with http invocation"
		case service.Function.TimeoutMS != 0:
			detail = "an opaque image's host.json execution timeout cannot be enforced through ARM"
		case service.Function.Architecture != "" && service.Function.Architecture != "amd64":
			detail = "the initial native hosting contract supports amd64 images only"
		case len(service.Entrypoint) != 0 || len(service.Command) != 0:
			detail = "entrypoint and command overrides can bypass the Functions host"
		case service.Update != nil:
			detail = "container rollout directives are unsupported; native single-revision convergence is required"
		case service.Replicas != 1:
			detail = "container replica counts do not express function event concurrency"
		case !image.MatchString(service.Image):
			detail = "a digest-pinned Functions v4 image is required"
		}
		if detail != "" {
			rejections = append(rejections, provider.Rejection{Service: service.Name, Feature: provider.FeatureFunction, Detail: detail})
		}
		for _, env := range service.Env {
			if strings.HasPrefix(strings.ToLower(env.Key), "azurewebjobsstorage") || strings.EqualFold(env.Key, "FUNCTIONS_EXTENSION_VERSION") {
				rejections = append(rejections, provider.Rejection{Service: service.Name, Feature: provider.FeatureFunction, Detail: "reserved host setting " + env.Key + " is supplied by the verified target storage/runtime contract"})
			}
		}
	}
	if len(rejections) != 0 {
		return &provider.RejectionError{Provider: "azurefunctions", Rejections: rejections}
	}
	return nil
}
