package lambda

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
)

type FunctionState struct {
	Live      *awslambda.GetFunctionOutput
	Published *awslambda.GetFunctionOutput
}

func (f FunctionState) Healthy() bool {
	live, published := f.configuration(f.Live), f.configuration(f.Published)
	return f.healthy(live, published)
}
func (f FunctionState) healthy(live, published types.FunctionConfiguration) bool {
	return live.State == types.StateActive && live.LastUpdateStatus == types.LastUpdateStatusSuccessful && published.State == types.StateActive
}
func (f FunctionState) Deployed() bool {
	live, published := f.configuration(f.Live), f.configuration(f.Published)
	return f.deployed(live, published)
}
func (f FunctionState) deployed(live, published types.FunctionConfiguration) bool {
	return (live.State == types.StateActive || live.State == types.StateInactive) && live.LastUpdateStatus == types.LastUpdateStatusSuccessful && (published.State == types.StateActive || published.State == types.StateInactive)
}
func (f FunctionState) Health() provider.Health {
	configuration := f.configuration(f.Live)
	healthy, deployed := f.Healthy(), f.Deployed()
	present := f.present()
	return f.health(configuration, present, healthy, deployed)
}
func (f FunctionState) health(configuration types.FunctionConfiguration, present, healthy, deployed bool) provider.Health {
	switch {
	case !present:
		return provider.Missing
	case configuration.State == types.StateFailed || configuration.LastUpdateStatus == types.LastUpdateStatusFailed:
		return provider.Degraded
	case configuration.State == types.StateInactive && deployed:
		return provider.Suspended
	case healthy:
		return provider.Healthy
	}
	return provider.Progressing
}
func (f FunctionState) Matches(target provider.Target, service spec.Service) bool {
	live, published := f.configuration(f.Live), f.configuration(f.Published)
	fields := []bool{f.Deployed(), f.matchesImage(f.Live, service), f.matchesConfiguration(&live, target, service), f.matchesImage(f.Published, service), f.matchesConfiguration(&published, target, service)}
	return f.all(fields)
}
func (FunctionState) all(fields []bool) bool {
	matches := true
	for _, field := range fields {
		matches = matches && field
	}
	return matches
}
func (FunctionState) configuration(live *awslambda.GetFunctionOutput) types.FunctionConfiguration {
	if live == nil || live.Configuration == nil {
		return types.FunctionConfiguration{}
	}
	return *live.Configuration
}
func (FunctionState) matchesImage(live *awslambda.GetFunctionOutput, service spec.Service) bool {
	return live != nil && live.Code != nil && aws.ToString(live.Code.ResolvedImageUri) == service.Image
}
func (FunctionState) matchesConfiguration(configuration *types.FunctionConfiguration, target provider.Target, service spec.Service) bool {
	return (configurationComparison{configuration: configuration, target: target, service: service}).Matches()
}
func (f FunctionState) Timeout(service spec.Service) int32 { return f.timeout(service) }
func (FunctionState) timeout(service spec.Service) int32 {
	if service.Function.TimeoutMS == 0 {
		return (functionLimits{}).DefaultTimeout()
	}
	return int32(service.Function.TimeoutMS / (spec.Function{}).MinimumTimeoutMS())
}
func (f FunctionState) Memory(service spec.Service) int32 { return f.memory(service) }
func (FunctionState) memory(service spec.Service) int32 {
	if service.Resources == nil || service.Resources.MemoryMiB == 0 {
		return int32((functionLimits{}).MinimumMemory())
	}
	return int32(service.Resources.MemoryMiB)
}
func (f FunctionState) Architecture(service spec.Service) types.Architecture {
	return f.architecture(service)
}
func (FunctionState) architecture(service spec.Service) types.Architecture {
	if service.Function.Architecture == "arm64" {
		return types.ArchitectureArm64
	}
	return types.ArchitectureX8664
}

func (f FunctionState) present() bool { return f.Live != nil && f.Live.Configuration != nil }
