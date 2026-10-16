package lambda

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/d31ma/heimdall/app/server/models/spec"
	"github.com/d31ma/heimdall/app/server/services/provider"
	"slices"
)

type configurationComparison struct {
	configuration *types.FunctionConfiguration
	target        provider.Target
	service       spec.Service
}

func (c configurationComparison) Matches() bool {
	native := c.native()
	image, environment := c.image(native), c.environment(native)
	desired := (FunctionState{})
	timeout, memory, architecture := desired.Timeout(c.service), desired.Memory(c.service), desired.Architecture(c.service)
	fields := []bool{c.host(native, timeout, memory, architecture), c.errors(native), c.imageMatches(image), c.environmentMatches(environment)}
	return (FunctionState{}).all(fields)
}
func (c configurationComparison) native() types.FunctionConfiguration {
	if c.configuration == nil {
		return types.FunctionConfiguration{}
	}
	return *c.configuration
}
func (c configurationComparison) host(native types.FunctionConfiguration, timeout, memory int32, architecture types.Architecture) bool {
	return native.PackageType == types.PackageTypeImage && aws.ToString(native.Role) == c.target.Config[(TargetContract{}).ExecutionRoleKey()] && aws.ToInt32(native.Timeout) == timeout && aws.ToInt32(native.MemorySize) == memory && slices.Equal(native.Architectures, []types.Architecture{architecture}) && native.CapacityProviderConfig == nil && native.DurableConfig == nil
}
func (configurationComparison) errors(native types.FunctionConfiguration) bool {
	return (native.Environment == nil || native.Environment.Error == nil) && (native.ImageConfigResponse == nil || native.ImageConfigResponse.Error == nil)
}
func (configurationComparison) image(native types.FunctionConfiguration) types.ImageConfig {
	if native.ImageConfigResponse == nil || native.ImageConfigResponse.ImageConfig == nil {
		return types.ImageConfig{}
	}
	return *native.ImageConfigResponse.ImageConfig
}
func (configurationComparison) environment(native types.FunctionConfiguration) map[string]string {
	if native.Environment == nil {
		return nil
	}
	return native.Environment.Variables
}
func (c configurationComparison) imageMatches(image types.ImageConfig) bool {
	return slices.Equal(image.Command, c.service.Command) && slices.Equal(image.EntryPoint, c.service.Entrypoint) && aws.ToString(image.WorkingDirectory) == ""
}
func (c configurationComparison) environmentMatches(environment map[string]string) bool {
	entries := c.entriesMatch(environment)
	return c.environmentSize(environment, entries)
}
func (c configurationComparison) environmentSize(environment map[string]string, entries bool) bool {
	return len(environment) == len(c.service.Env) && entries
}
func (c configurationComparison) entriesMatch(environment map[string]string) bool {
	entries := []bool{}
	for _, setting := range c.service.Env {
		value, present := environment[setting.Key]
		entries = append(entries, c.entryMatches(setting, value, present))
	}
	return (FunctionState{}).all(entries)
}
func (configurationComparison) entryMatches(setting spec.EnvVar, value string, present bool) bool {
	return present && (setting.Ref != "" || value == setting.Value)
}
