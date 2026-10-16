package azurefunctions

import (
	"regexp"
	"strings"

	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type functionValidation struct{}

func (v functionValidation) rejections(service spec.Service) []provider.Rejection {
	detail := v.detail(service)
	rejections := v.declaration(service.Name, detail)
	return append(rejections, v.environment(service)...)
}
func (functionValidation) detail(service spec.Service) string {
	contract := functionContract{}
	switch {
	case service.Function == nil:
		return "declare x-heimdall-function; ordinary container images are not Functions workloads"
	case service.Function.RuntimeContract != contract.runtime() || service.Function.Invocation != contract.invocation():
		return "requires azure-functions-v4 with http invocation"
	case service.Function.TimeoutMS != 0:
		return "an opaque image's host.json execution timeout cannot be enforced through ARM"
	case service.Function.Architecture != "" && service.Function.Architecture != contract.architecture():
		return "the initial native hosting contract supports amd64 images only"
	case len(service.Entrypoint) != 0 || len(service.Command) != 0:
		return "entrypoint and command overrides can bypass the Functions host"
	case service.Update != nil:
		return "container rollout directives are unsupported; native single-revision convergence is required"
	case service.Replicas != contract.declaredReplicas():
		return "container replica counts do not express function event concurrency"
	case !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[a-f0-9]{64}$`).MatchString(service.Image):
		return "a digest-pinned Functions v4 image is required"
	}
	return ""
}
func (functionValidation) declaration(service, detail string) []provider.Rejection {
	if detail == "" {
		return nil
	}
	return []provider.Rejection{{Service: service, Feature: provider.FeatureFunction, Detail: detail}}
}
func (v functionValidation) environment(service spec.Service) []provider.Rejection {
	rejections := []provider.Rejection{}
	for _, environment := range service.Env {
		rejections = append(rejections, v.reserved(service.Name, environment.Key)...)
	}
	return rejections
}
func (functionValidation) reserved(service, key string) []provider.Rejection {
	contract := functionContract{}
	if strings.HasPrefix(strings.ToLower(key), contract.storagePrefix()) || strings.EqualFold(key, contract.extensionSetting()) {
		return []provider.Rejection{{Service: service, Feature: provider.FeatureFunction, Detail: "reserved host setting " + key + " is supplied by the verified target storage/runtime contract"}}
	}
	return nil
}
