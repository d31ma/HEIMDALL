package cloudrun

import (
	"fmt"
	"slices"

	run "google.golang.org/api/run/v2"

	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
)

type RuntimeContract struct{}

func (c RuntimeContract) Validate(want spec.DeploySpec) error {
	return c.validationError(c.rejections(want))
}

func (c RuntimeContract) rejections(want spec.DeploySpec) []provider.Rejection {
	var rejected []provider.Rejection
	for _, service := range want.Services {
		rejected = append(rejected, c.serviceRejections(service)...)
	}
	return rejected
}

func (c RuntimeContract) serviceRejections(service spec.Service) []provider.Rejection {
	var rejected []provider.Rejection
	for _, port := range service.Ports {
		rejected = append(rejected, c.protocolRejection(service.Name, port)...)
	}
	return rejected
}

func (c RuntimeContract) protocolRejection(service string, port spec.Port) []provider.Rejection {
	if port.Protocol == "tcp" || port.Protocol == "" {
		return nil
	}
	return []provider.Rejection{{Service: service, Feature: provider.FeaturePorts,
		Detail: fmt.Sprintf("ports target %d uses %s; Cloud Run ingress requires HTTP over TCP", port.Target, port.Protocol)}}
}

func (c RuntimeContract) validationError(rejections []provider.Rejection) error {
	if len(rejections) == 0 {
		return nil
	}
	return &provider.RejectionError{Provider: "cloudrun", Rejections: rejections}
}

func (c RuntimeContract) Matches(want spec.Service, deployed *run.GoogleCloudRunV2Service) bool {
	return c.containerMatches(want, c.container(deployed))
}

func (c RuntimeContract) container(deployed *run.GoogleCloudRunV2Service) *run.GoogleCloudRunV2Container {
	if deployed == nil || deployed.Template == nil || len(deployed.Template.Containers) != 1 {
		return nil
	}
	return deployed.Template.Containers[0]
}

func (c RuntimeContract) containerMatches(want spec.Service, deployed *run.GoogleCloudRunV2Container) bool {
	if deployed == nil {
		return false
	}
	return deployed.Image == want.Image && slices.Equal(deployed.Command, want.Entrypoint) && slices.Equal(deployed.Args, want.Command)
}
