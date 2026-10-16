package azurefunctions

import (
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"github.com/d31ma/heimdall/app/server/utils/sequence"
)

type workloadValidator struct {
	want       spec.DeploySpec
	rejections []provider.Rejection
}

func (workloadValidator) validate(want spec.DeploySpec) error {
	validation := workloadValidator{want: want}
	return (sequence.Steps{Actions: []func() error{validation.capabilities, validation.services, validation.result}}).Run()
}
func (v workloadValidator) capabilities() error {
	return provider.Validate((capabilitySet{}).build(), v.want)
}
func (v *workloadValidator) services() error {
	for _, service := range v.want.Services {
		v.rejections = append(v.rejections, (functionValidation{}).rejections(service)...)
	}
	return nil
}
func (v *workloadValidator) result() error {
	if len(v.rejections) != 0 {
		return &provider.RejectionError{Provider: (functionContract{}).providerName(), Rejections: v.rejections}
	}
	return nil
}
