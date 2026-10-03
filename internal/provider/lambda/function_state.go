package lambda

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/internal/provider"
	"github.com/d31ma/heimdall/internal/spec"
	"slices"
)

type FunctionState struct {
	Live      *awslambda.GetFunctionOutput
	Published *awslambda.GetFunctionOutput
}

func (f FunctionState) Healthy() bool {
	return f.healthy()
}

func (f FunctionState) healthy() bool {
	c := f.Live.Configuration
	return c != nil && c.State == types.StateActive && c.LastUpdateStatus == types.LastUpdateStatusSuccessful && f.Published != nil && f.Published.Configuration != nil && f.Published.Configuration.State == types.StateActive
}

func (f FunctionState) Deployed() bool {
	return f.deployed()
}

func (f FunctionState) deployed() bool {
	c := f.Live.Configuration
	return c != nil && (c.State == types.StateActive || c.State == types.StateInactive) && c.LastUpdateStatus == types.LastUpdateStatusSuccessful && f.Published != nil && f.Published.Configuration != nil && (f.Published.Configuration.State == types.StateActive || f.Published.Configuration.State == types.StateInactive)
}

func (f FunctionState) Health() provider.Health {
	return f.health()
}

func (f FunctionState) health() provider.Health {
	c := f.Live.Configuration
	if c == nil {
		return provider.Missing
	}
	if c.State == types.StateFailed || c.LastUpdateStatus == types.LastUpdateStatusFailed {
		return provider.Degraded
	}
	if c.State == types.StateInactive && f.Deployed() {
		return provider.Suspended
	}
	if f.Healthy() {
		return provider.Healthy
	}
	return provider.Progressing
}

func (f FunctionState) Matches(target provider.Target, s spec.Service) bool {
	return f.matches(target, s)
}

func (f FunctionState) matches(target provider.Target, s spec.Service) bool {
	return f.Deployed() && f.matchesImage(f.Live, s) && f.matchesConfiguration(f.Live.Configuration, target, s) && f.matchesImage(f.Published, s) && f.matchesConfiguration(f.Published.Configuration, target, s)
}

func (f FunctionState) matchesImage(live *awslambda.GetFunctionOutput, s spec.Service) bool {
	return live != nil && live.Code != nil && aws.ToString(live.Code.ResolvedImageUri) == s.Image
}

func (f FunctionState) matchesConfiguration(c *types.FunctionConfiguration, target provider.Target, s spec.Service) bool {
	if c == nil || c.PackageType != types.PackageTypeImage || aws.ToString(c.Role) != target.Config["execution_role_arn"] || aws.ToInt32(c.Timeout) != f.Timeout(s) || aws.ToInt32(c.MemorySize) != f.Memory(s) || !slices.Equal(c.Architectures, []types.Architecture{f.Architecture(s)}) || c.CapacityProviderConfig != nil || c.DurableConfig != nil {
		return false
	}
	if c.Environment != nil && c.Environment.Error != nil || c.ImageConfigResponse != nil && c.ImageConfigResponse.Error != nil {
		return false
	}
	image := types.ImageConfig{}
	if c.ImageConfigResponse != nil && c.ImageConfigResponse.ImageConfig != nil {
		image = *c.ImageConfigResponse.ImageConfig
	}
	if !slices.Equal(image.Command, s.Command) || !slices.Equal(image.EntryPoint, s.Entrypoint) || aws.ToString(image.WorkingDirectory) != "" {
		return false
	}
	env := map[string]string{}
	if c.Environment != nil {
		env = c.Environment.Variables
	}
	if len(env) != len(s.Env) {
		return false
	}
	for _, v := range s.Env {
		value, ok := env[v.Key]
		if !ok || v.Ref == "" && value != v.Value {
			return false
		}
	}
	return true
}

func (f FunctionState) Timeout(s spec.Service) int32 {
	return f.timeout(s)
}

func (f FunctionState) timeout(s spec.Service) int32 {
	if s.Function.TimeoutMS == 0 {
		return 3
	}
	return int32(s.Function.TimeoutMS / 1000)
}

func (f FunctionState) Memory(s spec.Service) int32 {
	return f.memory(s)
}

func (f FunctionState) memory(s spec.Service) int32 {
	if s.Resources == nil || s.Resources.MemoryMiB == 0 {
		return 128
	}
	return int32(s.Resources.MemoryMiB)
}

func (f FunctionState) Architecture(s spec.Service) types.Architecture {
	return f.architecture(s)
}

func (f FunctionState) architecture(s spec.Service) types.Architecture {
	if s.Function.Architecture == "arm64" {
		return types.ArchitectureArm64
	}
	return types.ArchitectureX8664
}
